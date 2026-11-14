// Command factory is a cozy factory game for Ghostty (and any terminal).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const version = "0.1.0"

func home() string {
	if d := os.Getenv("FACTORY_HOME"); d != "" {
		return d
	}
	h, _ := os.UserHomeDir()
	return filepath.Join(h, ".factory")
}

func main() {
	args := os.Args[1:]
	newSave, forceBlocks := false, false
	for i, a := range args {
		switch a {
		case "msg":
			cmdMsg(args[i+1:])
			return
		case "hook":
			cmdHook(os.Stdin)
			return
		case "--version", "-v":
			fmt.Println("factory", version)
			return
		case "--help", "-h", "help":
			fmt.Print(usage)
			return
		case "--new":
			newSave = true
		case "--blocks":
			forceBlocks = true
		default:
			fmt.Fprintf(os.Stderr, "factory: unknown argument %q\n%s", a, usage)
			os.Exit(2)
		}
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		fmt.Fprintln(os.Stderr, "factory: needs a terminal")
		os.Exit(1)
	}
	if err := run(newSave, forceBlocks); err != nil {
		fmt.Fprintln(os.Stderr, "factory:", err)
		os.Exit(1)
	}
}

const usage = `factory - a cozy factory game for your terminal (best in Ghostty)

  factory              continue your save (~/.factory/save.json)
  factory --new        start over (the old save is kept as save.json.bak)
  factory --blocks     force the text renderer instead of pixel graphics
  factory msg [--from NAME] TEXT...   send a message into the game
  factory hook         Claude Code hook: reads hook JSON on stdin
`

// --- agent messages ---

type Msg struct{ From, Text string }

func inbox() string { return filepath.Join(home(), "inbox") }

func writeMsg(from, text string) error {
	if err := os.MkdirAll(inbox(), 0o755); err != nil {
		return err
	}
	text = strings.ReplaceAll(text, `\n`, "\n")
	name := filepath.Join(inbox(), strconv.FormatInt(time.Now().UnixNano(), 10)+".md")
	return os.WriteFile(name, []byte("From: "+from+"\n"+text), 0o644)
}

func cwdName() string {
	wd, _ := os.Getwd()
	return filepath.Base(wd)
}

func cmdMsg(args []string) {
	from := cwdName()
	if len(args) >= 2 && args[0] == "--from" {
		from, args = args[1], args[2:]
	}
	text := strings.Join(args, " ")
	if text == "" {
		b, _ := io.ReadAll(os.Stdin)
		text = string(b)
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintln(os.Stderr, "factory msg: nothing to send")
		os.Exit(2)
	}
	if err := writeMsg(from, text); err != nil {
		fmt.Fprintln(os.Stderr, "factory msg:", err)
		os.Exit(1)
	}
}

// cmdHook turns a Claude Code Stop/Notification hook into a message. It never
// fails loudly: a broken hook must not get in the agent's way.
func cmdHook(r io.Reader) {
	var h struct {
		Event   string `json:"hook_event_name"`
		Cwd     string `json:"cwd"`
		Last    string `json:"last_assistant_message"`
		Message string `json:"message"`
	}
	if json.NewDecoder(r).Decode(&h) != nil {
		return
	}
	from := filepath.Base(h.Cwd)
	if h.Cwd == "" {
		from = "claude"
	}
	switch h.Event {
	case "Stop":
		t := h.Last
		if t == "" {
			t = "finished"
		}
		writeMsg(from, trunc(t, 500))
	case "Notification":
		writeMsg(from, "needs you: "+trunc(h.Message, 300))
	}
}

func trunc(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}

func readInbox() []Msg {
	files, _ := filepath.Glob(filepath.Join(inbox(), "*"))
	sort.Strings(files)
	var out []Msg
	for _, f := range files {
		b, err := os.ReadFile(f)
		os.Remove(f)
		if err != nil || len(b) == 0 {
			continue
		}
		m := Msg{From: "inbox", Text: string(b)}
		if rest, ok := strings.CutPrefix(m.Text, "From: "); ok {
			m.From, m.Text, _ = strings.Cut(rest, "\n")
		}
		out = append(out, m)
	}
	return out
}

// --- the app ---

type App struct {
	g          *Game
	scr        Screen
	r          *Renderer
	cols, rows int
	cw, ch     int // pixels per cell (1,2 in blocks mode)
	zoom       int
	camX, camY int
	curX, curY int
	sel        Kind
	dir        int
	paused     bool
	help       bool
	msgs       []Msg
	note       string
	noteAt     time.Time
	dragLast   [2]int
	dragBtn    int
	toolbar    [numKinds][2]int // toolbar x ranges for clicks
	t0         time.Time
	lastPct    int
	ghostty    bool
	savePath   string
	realCW     int
	realCH     int
}

const sidebarW = 34

func (a *App) layout() (mapCols, mapRows, sb int) {
	if a.cols >= 90 {
		sb = sidebarW
	}
	return a.cols - sb, max(1, a.rows-3), sb
}

func (a *App) tileCells() (int, int) { return 2 * a.zoom, a.zoom }

func (a *App) follow() {
	mc, mr, _ := a.layout()
	tc, tr := a.tileCells()
	vw, vh := max(1, mc/tc), max(1, mr/tr)
	m := min(3, vw/4)
	n := min(3, vh/4)
	if a.curX < a.camX+m {
		a.camX = a.curX - m
	}
	if a.curX > a.camX+vw-1-m {
		a.camX = a.curX - vw + 1 + m
	}
	if a.curY < a.camY+n {
		a.camY = a.curY - n
	}
	if a.curY > a.camY+vh-1-n {
		a.camY = a.curY - vh + 1 + n
	}
}

func (a *App) center() {
	mc, mr, _ := a.layout()
	tc, tr := a.tileCells()
	a.camX, a.camY = a.curX-mc/tc/2, a.curY-mr/tr/2
}

func (a *App) say(s string) { a.note, a.noteAt = s, time.Now() }

func (a *App) placeSel(x, y, dir int) {
	if a.sel == Empty {
		return
	}
	if !a.g.unlocked(a.sel) {
		a.say(fmt.Sprintf("%s unlocks at %s earned", kindName[a.sel], money(kindUnlock[a.sel])))
		return
	}
	if !a.g.place(x, y, a.sel, dir) {
		a.say("Not enough money")
	}
}

func (a *App) move(d int, lay bool) {
	nx, ny := a.curX+dx[d], a.curY+dy[d]
	if !a.g.in(nx, ny) {
		return
	}
	if lay && a.sel != Empty {
		a.dir = d
		if b := a.g.at(a.curX, a.curY); b == nil || b.Kind == a.sel {
			a.placeSel(a.curX, a.curY, d)
		}
		a.placeSel(nx, ny, d)
	}
	a.curX, a.curY = nx, ny
	a.follow()
}

func (a *App) setZoom(z int) {
	z = max(1, min(6, z))
	if z != a.zoom {
		a.zoom = z
		a.resize()
		a.center()
	}
}

func (a *App) resize() {
	s := getSize()
	a.cols, a.rows = s.cols, s.rows
	a.cw, a.ch = 1, 2
	if a.scr.mode != gfxBlocks {
		a.cw, a.ch = a.realCW, a.realCH
		if s.cw > 0 {
			a.cw, a.ch = s.cw, s.ch
		}
	}
	tc, tr := a.tileCells()
	a.r.setTile(tc*a.cw, tr*a.ch)
	a.scr.invalidate()
	os.Stdout.WriteString("\x1b[2J\x1b_Ga=d,d=A,q=2\x1b\\")
	a.scr.imgID = 0
	a.follow()
}

type termSize struct{ cols, rows, cw, ch int }

func getSize() termSize {
	ws, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 {
		return termSize{80, 24, 0, 0}
	}
	s := termSize{cols: int(ws.Col), rows: int(ws.Row)}
	if ws.Xpixel > 0 && ws.Ypixel > 0 {
		s.cw, s.ch = int(ws.Xpixel)/s.cols, int(ws.Ypixel)/s.rows
	}
	return s
}

func run(newSave, forceBlocks bool) error {
	if err := os.MkdirAll(home(), 0o755); err != nil {
		return err
	}
	a := &App{r: newRenderer(), zoom: 2, dir: 1, sel: Belt, t0: time.Now(), lastPct: -1}
	a.savePath = filepath.Join(home(), "save.json")
	a.ghostty = os.Getenv("TERM_PROGRAM") == "ghostty" || strings.Contains(os.Getenv("TERM"), "ghostty")

	if newSave {
		os.Rename(a.savePath, a.savePath+".bak")
	}
	g, bonus, away, err := load(a.savePath)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("cannot read %s (%v); move it away or run with --new", a.savePath, err)
		}
		g = newGame(time.Now().UnixNano())
		a.help = true
	}
	a.g = g
	g.onEvent = a.say
	a.curX, a.curY = g.W/2, g.H/2

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	restore := func() {
		os.Stdout.WriteString("\x1b_Ga=d,d=A,q=2\x1b\\\x1b[?1006l\x1b[?1002l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l")
		if a.ghostty {
			os.Stdout.WriteString("\x1b]9;4;0\x07")
		}
		term.Restore(int(os.Stdin.Fd()), old)
		cleanupTemp()
	}
	defer func() {
		if p := recover(); p != nil {
			g.save(a.savePath)
			restore()
			panic(p)
		}
	}()
	os.Stdout.WriteString("\x1b[?1049h\x1b[?25l\x1b[2J")
	mode, pcw, pch := probe(os.Stdin)
	if forceBlocks {
		mode = gfxBlocks
	}
	a.realCW, a.realCH = pcw, pch
	if s := getSize(); s.cw > 0 {
		a.realCW, a.realCH = s.cw, s.ch
	}
	if a.realCW == 0 {
		mode = gfxBlocks
	}
	a.scr.mode = mode
	if mode == gfxBlocks {
		a.zoom = 3
	}
	os.Stdout.WriteString("\x1b[?1000h\x1b[?1002h\x1b[?1006h")
	a.resize()
	a.center()
	if bonus > 0 {
		a.say(fmt.Sprintf("While you were away (%s) the factory earned %s", away.Round(time.Minute), money(bonus)))
	}

	input := make(chan []byte, 64)
	go func() {
		for {
			buf := make([]byte, 4096)
			n, err := os.Stdin.Read(buf)
			if err != nil {
				close(input)
				return
			}
			input <- buf[:n]
		}
	}()
	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGTERM, syscall.SIGHUP)

	tick := time.NewTicker(time.Second / tps)
	defer tick.Stop()
	lastSave, lastInbox := time.Now(), time.Time{}
	quit := false
	for n := 0; !quit; n++ {
		select {
		case b, ok := <-input:
			if !ok {
				quit = true
				break
			}
			for _, e := range parseInput(b) {
				if a.handle(e) {
					quit = true
				}
			}
			continue // redraw on the next tick
		case s := <-sigs:
			if s == syscall.SIGWINCH {
				a.resize()
			} else {
				quit = true
			}
			continue
		case <-tick.C:
		}
		if !a.paused {
			g.update()
		}
		if time.Since(lastInbox) > time.Second {
			lastInbox = time.Now()
			for _, m := range readInbox() {
				a.msgs = append(a.msgs, m)
				if a.ghostty {
					os.Stdout.WriteString("\x1b]9;" + oscSafe(m.From+": "+trunc(m.Text, 120)) + "\x07")
				}
			}
			if len(a.msgs) > 20 {
				a.msgs = a.msgs[len(a.msgs)-20:]
			}
		}
		if time.Since(lastSave) > 30*time.Second {
			lastSave = time.Now()
			if err := g.save(a.savePath); err != nil {
				a.say("Save failed: " + err.Error())
			}
		}
		if a.scr.mode == gfxDirect && n%2 == 1 {
			continue // ~10 fps over the pty keeps ssh happy
		}
		a.draw()
	}
	err = g.save(a.savePath)
	restore()
	if err != nil {
		return fmt.Errorf("save failed: %v", err)
	}
	fmt.Printf("Saved. Earned %s so far.\n", money(g.Earned))
	return nil
}

var oscBad = regexp.MustCompile(`[\x00-\x1f\x7f;]+`)

func oscSafe(s string) string { return oscBad.ReplaceAllString(s, " ") }

// handle applies one input event; it returns true to quit.
func (a *App) handle(e Event) bool {
	g := a.g
	switch e.key {
	case "ctrl-c", "q":
		return true
	case "up", "w", "k":
		a.move(0, false)
	case "right", "d", "l":
		a.move(1, false)
	case "down", "s", "j":
		a.move(2, false)
	case "left", "a", "h":
		a.move(3, false)
	case "S-up", "W", "K":
		a.move(0, true)
	case "S-right", "D", "L":
		a.move(1, true)
	case "S-down", "S", "J":
		a.move(2, true)
	case "S-left", "A", "H":
		a.move(3, true)
	case "1", "2", "3", "4", "5":
		a.sel = Kind(e.key[0] - '0')
	case "0", "esc":
		a.sel = Empty
		a.help = false
	case " ", "enter":
		a.placeSel(a.curX, a.curY, a.dir)
	case "x", "backspace":
		g.remove(a.curX, a.curY)
	case "r", "R":
		step := 1
		if e.key == "R" {
			step = 3
		}
		if b := g.at(a.curX, a.curY); b != nil {
			b.Dir = (b.Dir + step) % 4
			a.dir = b.Dir
		} else {
			a.dir = (a.dir + step) % 4
		}
	case "f":
		g.cycleRecipe(g.at(a.curX, a.curY))
	case "+", "=":
		a.setZoom(a.zoom + 1)
	case "-", "_":
		a.setZoom(a.zoom - 1)
	case "p":
		a.paused = !a.paused
	case "?":
		a.help = !a.help
	case "n":
		a.msgs = nil
	case "c":
		a.center()
	case "ctrl-s":
		if err := g.save(a.savePath); err != nil {
			a.say("Save failed: " + err.Error())
		} else {
			a.say("Saved")
		}
	case "mouse":
		a.mouse(e)
	}
	return false
}

func (a *App) mouse(e Event) {
	mc, mr, _ := a.layout()
	if e.btn == 64 || e.btn == 65 {
		if e.press {
			a.setZoom(a.zoom + map[int]int{64: 1, 65: -1}[e.btn])
		}
		return
	}
	if !e.press {
		a.dragBtn = -1
		return
	}
	if e.my == a.rows-2 && !e.mov {
		for k := Belt; k < numKinds; k++ {
			if e.mx >= a.toolbar[k][0] && e.mx < a.toolbar[k][1] {
				a.sel = k
			}
		}
		return
	}
	if e.mx >= mc || e.my < 1 || e.my > mr {
		return
	}
	tc, tr := a.tileCells()
	tx, ty := a.camX+e.mx/tc, a.camY+(e.my-1)/tr
	if !a.g.in(tx, ty) {
		return
	}
	if !e.mov {
		a.dragBtn = e.btn
		a.dragLast = [2]int{tx, ty}
	} else if e.btn != a.dragBtn || (a.dragLast == [2]int{tx, ty}) {
		return
	}
	a.curX, a.curY = tx, ty
	switch e.btn {
	case 0:
		lx, ly := a.dragLast[0], a.dragLast[1]
		if e.mov && abs(tx-lx)+abs(ty-ly) == 1 {
			for d := 0; d < 4; d++ {
				if lx+dx[d] == tx && ly+dy[d] == ty {
					a.dir = d
				}
			}
			if b := a.g.at(lx, ly); b != nil && b.Kind == a.sel {
				b.Dir = a.dir // bend the previous tile toward the drag
			}
		}
		a.placeSel(tx, ty, a.dir)
	case 2:
		a.g.remove(tx, ty)
	}
	a.dragLast = [2]int{tx, ty}
}

// --- text UI ---

func fg(h uint32) string { return fmt.Sprintf("\x1b[38;2;%d;%d;%dm", h>>16&255, h>>8&255, h&255) }
func bg(h uint32) string { return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", h>>16&255, h>>8&255, h&255) }

const reset = "\x1b[0m"

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func vlen(s string) int { return utf8.RuneCountInString(ansiRe.ReplaceAllString(s, "")) }

// put writes s at row,col clipped/padded to width w on background b.
func put(w *bytes.Buffer, row, col, width int, b string, s string) {
	fmt.Fprintf(w, "\x1b[%d;%dH%s", row+1, col+1, b)
	n := 0
	for len(s) > 0 && n < width {
		if loc := ansiRe.FindStringIndex(s); loc != nil && loc[0] == 0 {
			w.WriteString(s[:loc[1]])
			w.WriteString(b) // keep the panel background after resets
			s = s[loc[1]:]
			continue
		}
		r, sz := utf8.DecodeRuneInString(s)
		w.WriteRune(r)
		s = s[sz:]
		n++
	}
	w.WriteString(strings.Repeat(" ", max(0, width-n)) + reset)
}

func money(v int) string {
	s := strconv.Itoa(abs(v))
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if v < 0 {
		return "-$" + s
	}
	return "$" + s
}

var itemHex = [numItems]uint32{0, 0xd9824a, 0x8fb3d9, 0x77777f, 0xa8a29a, 0xe39a62, 0xc4ccd8, 0xa4483a, 0xb8c0cc, 0xe08a4a, 0x3fbf66, 0xd94a3a}
var kindHex = [numKinds]uint32{0, 0x9aa3b2, 0xf2c14e, 0xff8c32, 0x6f9fe0, 0xe8b923}
var arrows = [4]string{"↑", "→", "↓", "←"}

const (
	panel  = 0x161a22
	ink    = 0xd8dee9
	muted  = 0x7b8494
	accent = 0x7fd1ff
)

func wrap(s string, w int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		para = strings.TrimSpace(strings.TrimLeft(para, "#>*- "))
		for utf8.RuneCountInString(para) > w {
			r := []rune(para)
			cut := w
			for i := w; i > w/2; i-- {
				if r[i] == ' ' {
					cut = i
					break
				}
			}
			out = append(out, string(r[:cut]))
			para = strings.TrimSpace(string(r[cut:]))
		}
		if para != "" {
			out = append(out, para)
		}
	}
	return out
}

func (a *App) sidebar(w int) []string {
	g := a.g
	var L []string
	add := func(s ...string) { L = append(L, s...) }
	head := func(s string) { add("", fg(accent)+"\x1b[1m"+s+reset) }
	pct := min(100, g.Earned*100/goal)
	bar := int(float64(w-4) * float64(pct) / 100)
	add(fg(0xffd479)+"\x1b[1m "+money(g.Money)+reset+fg(muted)+fmt.Sprintf("   +%s/min", money(int(g.rate()*60)))+reset,
		fg(muted)+" earned "+money(g.Earned)+" of "+money(goal)+reset,
		" "+fg(0x5fbf6a)+strings.Repeat("█", bar)+fg(0x2a303c)+strings.Repeat("█", w-4-bar)+reset)

	if a.help {
		head(" HOW TO PLAY")
		for _, s := range []string{
			"Drills mine the ore under them",
			"and push it out the yellow side.",
			"Belts carry items. Trade posts",
			"buy anything for money.",
			"Furnaces smelt ore into plates",
			"and burn 1 coal per 4 smelts.",
			"Assemblers craft parts (f picks",
			"the recipe). Earn $50,000!",
			"",
			"arrows/wasd  move cursor",
			"shift+arrows lay a line",
			"1-5  pick   0  inspect only",
			"space/click  build",
			"x/right-click  remove (refund)",
			"r R  rotate   f  recipe",
			"+ - wheel  zoom   c  center",
			"p pause   n clear msgs",
			"ctrl-s save   q quit   ? close",
		} {
			add(" " + fg(ink) + s + reset)
		}
		return L
	}

	head(" HERE")
	ground := "grass"
	if o := g.oreAt(a.curX, a.curY); o != None {
		ground = fg(itemHex[o]) + itemName[o] + fg(ink) + " field"
	}
	add(fmt.Sprintf(" %s%s%s  %s(%d,%d)%s", fg(ink), ground, reset, fg(muted), a.curX, a.curY, reset))
	if b := g.at(a.curX, a.curY); b != nil {
		dir := " " + arrows[b.Dir]
		if b.Kind == Trade {
			dir = ""
		}
		st := fg(0x5fbf6a) + "working"
		if b.Status != "" {
			st = fg(0xff6b6b) + b.Status
		} else if !b.working() {
			st = fg(muted) + "ok"
		}
		add(fmt.Sprintf(" %s\x1b[1m%s%s%s  %s%s", fg(kindHex[b.Kind]), kindName[b.Kind], dir, reset, st, reset))
		switch b.Kind {
		case Belt:
			if b.Item != None {
				add(" carrying " + fg(itemHex[b.Item]) + itemName[b.Item] + reset)
			}
		case Furnace:
			add(fmt.Sprintf(" %sfuel %d smelts, coal %d%s", fg(ink), b.Fuel, b.Inv[Coal], reset))
		case Assembler:
			r := asmRecipes[b.Recipe]
			var ins []string
			for _, s := range r.in {
				ins = append(ins, fmt.Sprintf("%d %s", s.n, itemName[s.item]))
			}
			add(fmt.Sprintf(" %s%s%s %s(f)%s", fg(ink), r.name, reset, fg(muted), reset),
				" "+fg(muted)+strings.Join(ins, " + ")+reset)
		case Trade:
			add(" " + fg(muted) + "sells anything it is given" + reset)
		}
		var inv []string
		for it := Item(1); it < numItems; it++ {
			if b.Inv[it] > 0 && !(b.Kind == Furnace && it == Coal) {
				inv = append(inv, fmt.Sprintf("%s%d %s", fg(itemHex[it]), b.Inv[it], itemName[it]))
			}
		}
		if len(inv) > 0 {
			add(" in  " + strings.Join(inv[:min(2, len(inv))], reset+", ") + reset)
		}
		if len(b.Out) > 0 {
			add(fmt.Sprintf(" out %s%d %s%s", fg(itemHex[b.Out[0]]), len(b.Out), itemName[b.Out[0]], reset))
		}
	} else if a.sel != Empty {
		add(fmt.Sprintf(" %sbuild %s%s%s %s, facing %s%s", fg(muted), fg(kindHex[a.sel]), kindName[a.sel], fg(muted), money(kindCost[a.sel]), arrows[a.dir], reset))
	}

	if g.Earned == 0 {
		head(" START HERE")
		for _, s := range []string{"2 Drill on ore, facing a belt", "1 Belts lead to a Trade post (5)", "Smelting pays more: 3 Furnace", "needs ore + coal fed into it", "? for all keys"} {
			add(" " + fg(ink) + s + reset)
		}
	} else {
		head(" UNLOCKS")
		for _, u := range []struct {
			n string
			v int
		}{{"Assembler", kindUnlock[Assembler]}, {"circuit recipe", asmRecipes[2].unlock}, {"engine recipe", asmRecipes[3].unlock}, {"the goal", goal}} {
			mark := fg(muted) + "  "
			if g.Earned >= u.v {
				mark = fg(0x5fbf6a) + "✓ "
			}
			add(fmt.Sprintf(" %s%s%-15s %s%s", mark, fg(ink), u.n, fg(muted), money(u.v)) + reset)
		}
	}

	if len(a.msgs) > 0 {
		head(" MESSAGES  " + fg(muted) + "n clears")
		for i := len(a.msgs) - 1; i >= 0 && len(L) < a.rows-4; i-- {
			m := a.msgs[i]
			add(" " + fg(0xc792ea) + "\x1b[1m" + trunc(m.From, w-3) + reset)
			for j, l := range wrap(m.Text, w-3) {
				if j == 4 {
					add("  " + fg(muted) + "…" + reset)
					break
				}
				add("  " + fg(ink) + l + reset)
			}
		}
	}
	return L
}

func (a *App) draw() {
	g := a.g
	mc, mr, sb := a.layout()
	var w bytes.Buffer
	w.WriteString("\x1b[?2026h")
	t := time.Since(a.t0).Seconds()

	f := a.r.draw(g, a.camX, a.camY, mc*a.cw, mr*a.ch, a.curX, a.curY, a.sel, a.dir, t)
	a.scr.drawImage(&w, f, 0, 1, mc, mr)

	// title bar
	title := fg(0xffd479) + "\x1b[1m ⚙ FACTORY " + reset + bg(0x0f1218) + fg(muted)
	if a.paused {
		title += fg(0xff6b6b) + " PAUSED " + fg(muted)
	}
	if a.note != "" && time.Since(a.noteAt) < 6*time.Second {
		title += " " + fg(ink) + a.note
	} else if sb == 0 {
		title += fmt.Sprintf(" %s  +%s/min", money(g.Money), money(int(g.rate()*60)))
	}
	put(&w, 0, 0, a.cols, bg(0x0f1218), title)

	if sb > 0 {
		lines := a.sidebar(sb - 2)
		for i := 0; i < mr; i++ {
			s := ""
			if i < len(lines) {
				s = lines[i]
			}
			put(&w, 1+i, mc, sb, bg(panel), fg(0x2a303c)+"▏"+reset+s)
		}
	}

	// toolbar
	var tb strings.Builder
	x := 1
	for k := Belt; k < numKinds; k++ {
		label := fmt.Sprintf(" %d %s %s ", k, kindName[k], money(kindCost[k]))
		col := fg(ink)
		if !g.unlocked(k) {
			label = fmt.Sprintf(" %d %s @%s ", k, kindName[k], money(kindUnlock[k]))
			col = fg(0x4c5566)
		}
		seg := fg(kindHex[k]) + "■" + col + label
		if k == a.sel {
			seg = bg(0x2e3a50) + seg + bg(0x0f1218)
		}
		a.toolbar[k] = [2]int{x, x + 1 + vlen(label)}
		x += 2 + vlen(label)
		tb.WriteString(seg + " ")
	}
	put(&w, a.rows-2, 0, a.cols, bg(0x0f1218), " "+tb.String())
	hints := "move ←↑→↓/wasd  lay ⇧+dir  build ␣/click  remove x  rotate r  recipe f  zoom ± " + arrows[a.dir] + "  help ?  quit q"
	put(&w, a.rows-1, 0, a.cols-1, bg(0x0f1218), fg(muted)+" "+hints) // never touch the bottom-right cell: some terminals scroll

	if a.ghostty {
		if pct := min(100, g.Earned*100/goal); pct != a.lastPct {
			a.lastPct = pct
			fmt.Fprintf(&w, "\x1b]9;4;1;%d\x07\x1b]2;factory · %s\x07", pct, money(g.Money))
		}
	}
	w.WriteString("\x1b[?2026l")
	os.Stdout.Write(w.Bytes())
}
