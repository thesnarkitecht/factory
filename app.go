package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

type App struct {
	g          *Game
	scr        Screen
	r          *Renderer
	cols, rows int
	cw, ch     int // pixels per cell (1,2 in blocks mode)
	realCW     int
	realCH     int
	view       View
	cur        [2]int
	sel        int // tower kind to build, -1 none
	paused     bool
	fast       bool
	help       bool
	msgs       []Msg
	note       string
	noteAt     time.Time
	toolbar    [numTower][2]int // toolbar x ranges for clicks
	clicks     map[int]string   // sidebar row -> key it acts like
	t0         time.Time
	ghostty    bool
	lastProg   string
}

const sidebarW = 36

func (a *App) layout() (mapCols, mapRows, sb int) {
	if a.cols >= 90 {
		sb = sidebarW
	}
	return a.cols - sb, max(1, a.rows-3), sb
}

func (a *App) say(s string) { a.note, a.noteAt = s, time.Now() }

func (a *App) newMatch() {
	a.g = newGame(time.Now().UnixNano())
	a.g.onEvent = a.say
	a.cur = [2]int{5, 2}
	a.sel = int(Gun)
	a.paused = false
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
	a.scr.invalidate()
	os.Stdout.WriteString("\x1b[2J\x1b_Ga=d,d=A,q=2\x1b\\")
	a.scr.imgID = 0
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

func run(forceBlocks bool) error {
	if err := os.MkdirAll(home(), 0o755); err != nil {
		return err
	}
	a := &App{r: newRenderer(), t0: time.Now(), help: true, clicks: map[int]string{}}
	a.ghostty = os.Getenv("TERM_PROGRAM") == "ghostty" || strings.Contains(os.Getenv("TERM"), "ghostty")
	a.newMatch()

	old, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return err
	}
	restore := func() {
		os.Stdout.WriteString("\x1b_Ga=d,d=A,q=2\x1b\\\x1b[?1006l\x1b[?1003l\x1b[?1002l\x1b[?1000l\x1b[0m\x1b[?25h\x1b[?1049l")
		if a.ghostty {
			os.Stdout.WriteString("\x1b]9;4;0\x07")
		}
		term.Restore(int(os.Stdin.Fd()), old)
		cleanupTemp()
	}
	defer func() {
		if p := recover(); p != nil {
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
	// 1003: report motion with no button held, so the cursor follows the mouse
	os.Stdout.WriteString("\x1b[?1000h\x1b[?1002h\x1b[?1003h\x1b[?1006h")
	a.resize()

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
	lastInbox := time.Time{}
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
			continue
		case s := <-sigs:
			if s == syscall.SIGWINCH {
				a.resize()
			} else {
				quit = true
			}
			continue
		case <-tick.C:
		}
		if !a.paused && !a.help {
			a.g.update()
			if a.fast {
				a.g.update()
			}
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
		if a.scr.mode == gfxDirect && n%2 == 1 {
			continue // ~10 fps over the pty keeps ssh happy
		}
		a.draw()
	}
	restore()
	g := a.g
	fmt.Printf("factory: round %d, %d bots stopped, %d sent.\n", g.Round, g.You.Kills, g.You.Sent)
	return nil
}

var oscBad = regexp.MustCompile(`[\x00-\x1f\x7f;]+`)

func oscSafe(s string) string { return oscBad.ReplaceAllString(s, " ") }

func (a *App) moveCur(d int) {
	x, y := a.cur[0]+dx[d], a.cur[1]+dy[d]
	if x >= 0 && y >= 0 && x < boardW && y < boardH {
		a.cur = [2]int{x, y}
	}
}

func (a *App) buildOrUpgrade() {
	b := a.g.You
	if a.g.Over != "" {
		return
	}
	if t := b.towerAt(a.cur[0], a.cur[1]); t != nil {
		a.upgrade()
		return
	}
	if a.sel < 0 {
		return
	}
	switch {
	case !buildable(a.cur[0], a.cur[1]):
		a.say("Can't build on the conveyor")
	case !b.build(TowerKind(a.sel), a.cur[0], a.cur[1]):
		a.say(fmt.Sprintf("Need %s for a %s", money(towerSpecs[a.sel].cost), towerSpecs[a.sel].name))
	}
}

func (a *App) upgrade() {
	t := a.g.You.towerAt(a.cur[0], a.cur[1])
	switch {
	case t == nil || a.g.Over != "":
	case t.Lvl >= maxLevel:
		a.say("That one is fully upgraded")
	case !a.g.You.upgrade(t):
		a.say("Need " + money(upgradeCost(t)) + " to upgrade")
	}
}

// handle applies one input event; it returns true to quit.
func (a *App) handle(e Event) bool {
	g := a.g
	if a.help && e.key != "mouse" && e.key != "ctrl-c" && e.key != "q" {
		a.help = false // any key starts the match
		return false
	}
	switch e.key {
	case "ctrl-c", "q":
		return true
	case "up", "w", "k":
		a.moveCur(0)
	case "right", "d", "l":
		a.moveCur(1)
	case "down", "s", "j":
		a.moveCur(2)
	case "left", "a", "h":
		a.moveCur(3)
	case "1", "2", "3", "4":
		k := int(e.key[0] - '1')
		if a.sel == k {
			k = -1
		}
		a.sel = k
	case "0", "esc":
		a.sel = -1
	case " ", "enter":
		if g.Over != "" {
			a.newMatch()
			return false
		}
		a.buildOrUpgrade()
	case "u":
		a.upgrade()
	case "x", "backspace":
		if t := g.You.towerAt(a.cur[0], a.cur[1]); t != nil && g.Over == "" {
			g.You.sell(t)
		}
	case "z", "c", "v", "b":
		for i, s := range sends {
			if s.key == e.key {
				switch {
				case g.Round < s.minRound:
					a.say(fmt.Sprintf("%ss can be sent from round %d", enemySpecs[s.kind].name, s.minRound))
				case !g.send(i, g.You):
					a.say("Need " + money(s.cost) + " to send that")
				}
			}
		}
	case "p":
		a.paused = !a.paused
	case "f":
		a.fast = !a.fast
	case "r":
		if g.Over != "" {
			a.newMatch()
		}
	case "?":
		a.help = true
	case "n":
		a.msgs = nil
	case "mouse":
		a.mouse(e)
	}
	return false
}

func (a *App) mouse(e Event) {
	mc, mr, _ := a.layout()
	if !e.press || e.btn >= 64 {
		return
	}
	if e.my == a.rows-2 && !e.mov {
		for k := 0; k < int(numTower); k++ {
			if e.mx >= a.toolbar[k][0] && e.mx < a.toolbar[k][1] {
				a.handle(Event{key: strconv.Itoa(k + 1)})
			}
		}
		return
	}
	if e.mx >= mc && !e.mov {
		if key, ok := a.clicks[e.my]; ok {
			a.handle(Event{key: key})
		}
		return
	}
	if e.my < 1 || e.my > mr || a.view.T == 0 {
		return
	}
	// cell center -> pixel -> tile on your board
	px, py := e.mx*a.cw+a.cw/2, (e.my-1)*a.ch+a.ch/2
	tx, ty := px/a.view.T, py/a.view.T
	if tx < 0 || ty < 0 || tx >= boardW || ty >= boardH {
		return
	}
	a.cur = [2]int{tx, ty}
	if e.mov {
		return // hover (btn 3) or drag just moves the cursor
	}
	switch e.btn {
	case 0:
		if a.help {
			a.help = false
			return
		}
		if a.g.Over != "" {
			a.newMatch()
			return
		}
		if a.g.You.towerAt(tx, ty) == nil {
			a.buildOrUpgrade()
		}
	case 2:
		a.sel = -1
	}
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

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

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

const (
	panel  = 0x161a22
	ink    = 0xd8dee9
	muted  = 0x7b8494
	accent = 0x7fd1ff
	good   = 0x5fbf6a
	bad    = 0xff6b6b
	cash   = 0xffd479
)

var towerHex = [numTower]uint32{0x5b8def, 0xb07a3a, 0xd9824a, 0x6fd6ff}
var enemyHex = [numEnemy]uint32{0xa87b4f, 0x5ec8ff, 0xd9534f, 0x8a94a6}

func hearts(n int) string {
	c := uint32(good)
	if n < 30 {
		c = bad
	}
	return fg(c) + "♥ " + strconv.Itoa(max(0, n)) + reset
}

func (a *App) sidebar(w int) []string {
	g := a.g
	var L []string
	a.clicks = map[int]string{}
	add := func(s string) { L = append(L, s) }
	act := func(key, s string) { a.clicks[1+len(L)] = key; add(s) }
	head := func(s string) { add(""); add(fg(accent) + "\x1b[1m " + s + reset) }

	if a.help {
		add(fg(cash) + "\x1b[1m DEFEND THE FACTORY" + reset)
		for _, s := range []string{
			"",
			"Scrap bots ride the conveyor",
			"toward your factory. Build",
			"towers beside it to stop them.",
			"",
			"Every 6s you get paid. Send bots",
			"to your rival (z c v b) to raise",
			"your pay. Their sends hit you.",
			"First factory to fall loses.",
			"",
			"arrows/mouse  move cursor",
			"1-4  pick a tower",
			"space/click  build or upgrade",
			"u upgrade  x sell  right-click",
			"  stop building",
			"f fast  p pause  ? help  q quit",
			"",
			fg(cash) + "Press any key to start" + fg(ink),
		} {
			add(" " + fg(ink) + s + reset)
		}
		return L
	}

	next := int(math.Ceil(g.nextRound - g.Time))
	add(fmt.Sprintf(" %s\x1b[1mROUND %d%s   %snext wave %ds%s", fg(ink), g.Round, reset, fg(muted), next, reset))
	pay := int(math.Ceil(g.nextPay - g.Time))
	add(fmt.Sprintf(" %s  %sYOU%s    %s%s%s  %s+%s in %ds%s", hearts(g.You.Lives), fg(ink), reset, fg(cash), money(g.You.Money), reset, fg(muted), money(g.You.Income), pay, reset))
	add(fmt.Sprintf(" %s  %sRIVAL%s  %s+%s pay, %d towers%s", hearts(g.Rival.Lives), fg(ink), reset, fg(muted), money(g.Rival.Income), len(g.Rival.Towers), reset))

	if g.Over != "" {
		head("")
		if g.Over == "win" {
			add(fg(good) + "\x1b[1m  YOU WIN! The rival's factory fell." + reset)
		} else {
			add(fg(bad) + "\x1b[1m  YOUR FACTORY FELL." + reset)
		}
		act("enter", fg(ink)+"  Enter or click: play again"+reset)
	}

	head("BUILD")
	for k := 0; k < int(numTower); k++ {
		s := towerSpecs[k]
		mark, col := " ", fg(ink)
		if a.sel == k {
			mark = fg(accent) + "▸"
		}
		if g.You.Money < s.cost {
			col = fg(muted)
		}
		act(strconv.Itoa(k+1), fmt.Sprintf("%s%s■ %s%d %-11s %s%5s%s", mark, fg(towerHex[k]), col, k+1, s.name, fg(cash), money(s.cost), reset))
	}

	if t := g.You.towerAt(a.cur[0], a.cur[1]); t != nil {
		s := t.stats()
		head(fmt.Sprintf("%s  level %d/%d", towerSpecs[t.Kind].name, t.Lvl, maxLevel))
		desc := fmt.Sprintf("dmg %.1f  range %.1f  %.1f/s", s.dmg, s.rng, s.rate)
		if s.slow > 0 {
			desc = fmt.Sprintf("slows to %d%%  range %.1f", int(s.slow*100), s.rng)
		}
		add(" " + fg(muted) + desc + reset)
		if t.Lvl < maxLevel {
			col := fg(ink)
			if g.You.Money < upgradeCost(t) {
				col = fg(muted)
			}
			act("u", fmt.Sprintf(" %s[u] upgrade %s%s", col, fg(cash), money(upgradeCost(t)))+reset)
		}
		act("x", fmt.Sprintf(" %s[x] sell for %s%s", fg(ink), fg(cash), money(sellValue(t)))+reset)
	} else if a.sel >= 0 {
		add("")
		add(" " + fg(muted) + towerSpecs[a.sel].name + ": " + towerSpecs[a.sel].blurb + reset)
	}

	head("SEND TO RIVAL  " + fg(muted) + "raises your pay")
	for _, s := range sends {
		col, tail := fg(ink), ""
		if g.Round < s.minRound {
			col, tail = fg(0x4c5566), fmt.Sprintf(" r%d", s.minRound)
		} else if g.You.Money < s.cost {
			col = fg(muted)
		}
		label := fmt.Sprintf("%d %s", s.count, enemySpecs[s.kind].name)
		if s.count > 1 {
			label += "s"
		}
		act(s.key, fmt.Sprintf(" %s[%s] %s■%s %-13s%s%5s %s+%d%s%s", col, s.key, fg(enemyHex[s.kind]), col, label, fg(cash), money(s.cost), fg(good), s.income, fg(muted)+tail, reset))
	}

	if len(a.msgs) > 0 {
		head("MESSAGES  " + fg(muted) + "n clears")
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

	sel := a.sel
	if g.Over != "" {
		sel = -1
	}
	f, v := a.r.draw(g, mc*a.cw, mr*a.ch, a.cur, sel, t, a.scr.mode != gfxBlocks)
	a.view = v
	a.scr.drawImage(&w, f, 0, 1, mc, mr)

	title := fg(cash) + "\x1b[1m ⚙ FACTORY " + reset + bg(0x0f1218) + fg(muted)
	if a.paused {
		title += fg(bad) + " PAUSED " + fg(muted)
	}
	if a.fast {
		title += fg(accent) + " ▸▸ FAST " + fg(muted)
	}
	switch {
	case g.Over == "win":
		title += fg(good) + "  You win! Enter for a rematch"
	case g.Over == "lose":
		title += fg(bad) + "  Your factory fell. Enter to try again"
	case a.note != "" && time.Since(a.noteAt) < 5*time.Second:
		title += " " + fg(ink) + a.note
	case sb == 0:
		title += fmt.Sprintf(" round %d  %s  %s  rival %s", g.Round, hearts(g.You.Lives), money(g.You.Money), hearts(g.Rival.Lives))
	default:
		if v.RivalT > 0 && v.RivalY == 0 {
			title += strings.Repeat(" ", max(1, v.RivalX/max(1, a.cw)-12)) + fg(muted) + "rival's board ▾"
		}
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

	var tb strings.Builder
	x := 1
	for k := 0; k < int(numTower); k++ {
		s := towerSpecs[k]
		label := fmt.Sprintf(" %d %s %s ", k+1, s.name, money(s.cost))
		col := fg(ink)
		if g.You.Money < s.cost {
			col = fg(0x4c5566)
		}
		seg := fg(towerHex[k]) + "■" + col + label
		if k == a.sel {
			seg = bg(0x2e3a50) + seg + bg(0x0f1218)
		}
		a.toolbar[k] = [2]int{x, x + 1 + vlen(label)}
		x += 2 + vlen(label)
		tb.WriteString(seg + " ")
	}
	put(&w, a.rows-2, 0, a.cols, bg(0x0f1218), " "+tb.String())
	hints := "move ←↑→↓/mouse  build/upgrade ␣/click  sell x  send z c v b  fast f  pause p  help ?  quit q"
	put(&w, a.rows-1, 0, a.cols-1, bg(0x0f1218), fg(muted)+" "+hints) // never touch the bottom-right cell: some terminals scroll

	if a.ghostty { // tab progress bar = your lives; red when low
		state := 1
		if g.You.Lives < 30 {
			state = 2
		}
		p := fmt.Sprintf("\x1b]9;4;%d;%d\x07\x1b]2;factory · round %d\x07", state, max(0, g.You.Lives), g.Round)
		if p != a.lastProg {
			a.lastProg = p
			w.WriteString(p)
		}
	}
	w.WriteString("\x1b[?2026l")
	os.Stdout.Write(w.Bytes())
}
