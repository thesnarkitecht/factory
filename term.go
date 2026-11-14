package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

type Event struct {
	key        string // "up", "S-left", "a", "enter", "mouse", ...
	mx, my     int    // 0-based cell
	btn        int    // 0 left, 1 middle, 2 right, 64/65 wheel
	press, mov bool
}

// parseInput splits raw terminal bytes into events. Unknown sequences are dropped.
func parseInput(b []byte) []Event {
	var ev []Event
	for i := 0; i < len(b); {
		c := b[i]
		if c != 0x1b {
			r, n := utf8.DecodeRune(b[i:])
			switch c {
			case 3:
				ev = append(ev, Event{key: "ctrl-c"})
			case 19:
				ev = append(ev, Event{key: "ctrl-s"})
			case 13, 10:
				ev = append(ev, Event{key: "enter"})
			case 127, 8:
				ev = append(ev, Event{key: "backspace"})
			default:
				ev = append(ev, Event{key: string(r)})
			}
			i += n
			continue
		}
		if i+1 >= len(b) {
			ev = append(ev, Event{key: "esc"})
			break
		}
		if b[i+1] == 'O' && i+2 < len(b) { // SS3 arrows
			if k, ok := arrow[b[i+2]]; ok {
				ev = append(ev, Event{key: k})
			}
			i += 3
			continue
		}
		if b[i+1] != '[' && b[i+1] != '_' {
			ev = append(ev, Event{key: "esc"})
			i++
			continue
		}
		if b[i+1] == '_' { // APC reply (graphics): skip to ST
			end := bytes.Index(b[i:], []byte("\x1b\\"))
			if end < 0 {
				break
			}
			i += end + 2
			continue
		}
		j := i + 2
		for j < len(b) && (b[j] < 0x40 || b[j] > 0x7e) {
			j++
		}
		if j >= len(b) {
			break
		}
		params, final := string(b[i+2:j]), b[j]
		i = j + 1
		if strings.HasPrefix(params, "<") && (final == 'M' || final == 'm') {
			p := strings.Split(params[1:], ";")
			if len(p) == 3 {
				code, _ := strconv.Atoi(p[0])
				x, _ := strconv.Atoi(p[1])
				y, _ := strconv.Atoi(p[2])
				ev = append(ev, Event{key: "mouse", btn: code &^ 32, mov: code&32 != 0, press: final == 'M', mx: x - 1, my: y - 1})
			}
			continue
		}
		if k, ok := arrow[final]; ok {
			if strings.HasSuffix(params, ";2") {
				k = "S-" + k
			}
			ev = append(ev, Event{key: k})
		}
	}
	return ev
}

var arrow = map[byte]string{'A': "up", 'B': "down", 'C': "right", 'D': "left"}

// --- capability probe ---

type gfxMode int

const (
	gfxBlocks gfxMode = iota // half-block truecolor text
	gfxDirect                // kitty graphics, zlib over the pty (works over ssh)
	gfxFile                  // kitty graphics, raw pixels via temp file (local, fastest)
)

const tmpPrefix = "factory-tty-graphics-protocol-"

// probe asks the terminal what it supports, ending with DA1 so unsupported
// terminals still answer. Must be called in raw mode.
func probe(in *os.File) (mode gfxMode, cw, ch int) {
	f, err := os.CreateTemp("", tmpPrefix+"*")
	path := ""
	if err == nil {
		f.Write([]byte{255, 0, 0})
		f.Close()
		path = f.Name()
		defer os.Remove(path)
	}
	q := "\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\"
	if path != "" {
		q += "\x1b_Gi=32,s=1,v=1,a=q,t=t,f=24;" + base64.StdEncoding.EncodeToString([]byte(path)) + "\x1b\\"
	}
	os.Stdout.WriteString(q + "\x1b[16t\x1b[c")
	var got []byte
	buf := make([]byte, 1024)
	deadline := time.Now().Add(time.Second)
	fd := int(in.Fd())
	for time.Now().Before(deadline) {
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		if n, _ := unix.Poll(fds, 100); n <= 0 {
			continue
		}
		n, err := in.Read(buf)
		if err != nil {
			break
		}
		got = append(got, buf[:n]...)
		if bytes.Contains(got, []byte("\x1b[?")) && bytes.HasSuffix(got, []byte("c")) {
			break
		}
	}
	s := string(got)
	if strings.Contains(s, "i=32;OK") {
		mode = gfxFile
	} else if strings.Contains(s, "i=31;OK") {
		mode = gfxDirect
	}
	if k := strings.Index(s, "\x1b[6;"); k >= 0 {
		fmt.Sscanf(s[k+4:], "%d;%dt", &ch, &cw)
	}
	return
}

// --- graphics output ---

type Screen struct {
	mode   gfxMode
	imgID  int
	prev   []uint8 // blocks mode: last frame for diffing
	prevW  int
	frames int
}

// drawImage places f at cell (col,row) spanning cols x rows cells.
func (s *Screen) drawImage(w *bytes.Buffer, f *Frame, col, row, cols, rows int) {
	if s.mode == gfxBlocks {
		s.drawBlocks(w, f, col, row)
		return
	}
	old := s.imgID
	s.imgID = 1 + s.imgID%2 // double buffer: place new, then delete old
	fmt.Fprintf(w, "\x1b[%d;%dH", row+1, col+1)
	hdr := fmt.Sprintf("a=T,f=24,s=%d,v=%d,i=%d,p=1,c=%d,r=%d,C=1,q=2", f.w, f.h, s.imgID, cols, rows)
	sent := false
	if s.mode == gfxFile {
		if t, err := os.CreateTemp("", tmpPrefix+"*"); err == nil {
			_, err1 := t.Write(f.px)
			t.Close()
			if err1 == nil {
				fmt.Fprintf(w, "\x1b_G%s,t=t;%s\x1b\\", hdr, base64.StdEncoding.EncodeToString([]byte(t.Name())))
				sent = true
			}
		}
	}
	if !sent {
		var z bytes.Buffer
		zw, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
		zw.Write(f.px)
		zw.Close()
		data := base64.StdEncoding.EncodeToString(z.Bytes())
		for i := 0; i < len(data); i += 4096 {
			end := min(i+4096, len(data))
			m := 0
			if end < len(data) {
				m = 1
			}
			if i == 0 {
				fmt.Fprintf(w, "\x1b_G%s,o=z,m=%d;%s\x1b\\", hdr, m, data[i:end])
			} else {
				fmt.Fprintf(w, "\x1b_Gm=%d,q=2;%s\x1b\\", m, data[i:end])
			}
		}
	}
	if old != 0 {
		fmt.Fprintf(w, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", old)
	}
}

// drawBlocks paints two pixels per cell with "▀", only where cells changed.
func (s *Screen) drawBlocks(w *bytes.Buffer, f *Frame, col, row int) {
	if len(s.prev) != len(f.px) || s.prevW != f.w {
		s.prev = make([]uint8, len(f.px))
		for i := range s.prev {
			s.prev[i] = ^f.px[i] // force full repaint
		}
		s.prevW = f.w
	}
	var lastFg, lastBg [3]uint8
	for cy := 0; cy < f.h/2; cy++ {
		cursorAt := -2
		for cx := 0; cx < f.w; cx++ {
			ti := ((2*cy)*f.w + cx) * 3
			bi := ((2*cy+1)*f.w + cx) * 3
			if bytes.Equal(f.px[ti:ti+3], s.prev[ti:ti+3]) && bytes.Equal(f.px[bi:bi+3], s.prev[bi:bi+3]) {
				continue
			}
			if cursorAt != cx {
				fmt.Fprintf(w, "\x1b[%d;%dH", row+cy+1, col+cx+1)
				lastFg, lastBg = [3]uint8{1, 2, 3}, [3]uint8{1, 2, 3}
			}
			fg, bg := [3]uint8(f.px[ti:ti+3]), [3]uint8(f.px[bi:bi+3])
			if fg != lastFg {
				fmt.Fprintf(w, "\x1b[38;2;%d;%d;%dm", fg[0], fg[1], fg[2])
			}
			if bg != lastBg {
				fmt.Fprintf(w, "\x1b[48;2;%d;%d;%dm", bg[0], bg[1], bg[2])
			}
			w.WriteString("▀")
			lastFg, lastBg, cursorAt = fg, bg, cx+1
		}
	}
	w.WriteString("\x1b[0m")
	copy(s.prev, f.px)
}

func (s *Screen) invalidate() { s.prev = nil }

// cleanupTemp removes temp frames the terminal never picked up.
func cleanupTemp() {
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), tmpPrefix+"*"))
	for _, p := range m {
		os.Remove(p)
	}
}
