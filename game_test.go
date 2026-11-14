package main

import (
	"image"
	"image/png"
	"math"
	"os"
	"strings"
	"testing"
)

func sim(g *Game, secs float64) {
	for i := 0; i < int(secs*tps); i++ {
		g.update()
	}
}

func TestPathConnected(t *testing.T) {
	p := pathOrder()
	for i := 1; i < len(p); i++ {
		if abs(p[i][0]-p[i-1][0])+abs(p[i][1]-p[i-1][1]) != 1 {
			t.Fatalf("path breaks at %v -> %v", p[i-1], p[i])
		}
	}
	if p[len(p)-1] != factoryTile {
		t.Fatalf("path ends at %v, factory at %v", p[len(p)-1], factoryTile)
	}
	if x, y := pos(pathLen); x != 16 || y != 10 {
		t.Fatalf("pos(end) = %v,%v", x, y)
	}
}

func TestSpritesAreSquare(t *testing.T) {
	for name, rows := range sprites {
		for i, r := range rows {
			if len(r) != 16 {
				t.Fatalf("%s row %d is %d wide", name, i, len(r))
			}
			for _, c := range []byte(r) {
				if _, ok := palette[c]; !ok && c != '.' && c != 'a' && c != 'A' {
					t.Fatalf("%s uses unknown color %q", name, c)
				}
			}
		}
	}
}

func TestUndefendedLeaks(t *testing.T) {
	g := newGame(1)
	g.ai.next = math.Inf(1) // rival idle
	sim(g, firstRound+45)
	if g.You.Lives >= startLives {
		t.Fatal("bots never reached the factory")
	}
}

func TestTowersDefendAndPay(t *testing.T) {
	g := newGame(1)
	g.ai.next = math.Inf(1)
	g.You.Money = 10000
	for _, xy := range [][2]int{{5, 2}, {8, 2}, {11, 2}, {6, 5}} {
		if !g.You.build(Gun, xy[0], xy[1]) {
			t.Fatalf("build at %v failed", xy)
		}
	}
	m := g.You.Money
	sim(g, firstRound+15) // round 1: scrap bots only
	if g.You.Lives != startLives {
		t.Fatalf("lost lives: %d", g.You.Lives)
	}
	if g.You.Kills == 0 || g.You.Money <= m {
		t.Fatalf("kills %d money %d (was %d)", g.You.Kills, g.You.Money, m)
	}
}

func TestBuildRules(t *testing.T) {
	b := newBoard()
	if b.build(Gun, 3, 1) {
		t.Fatal("built on the path")
	}
	if !b.build(Gun, 0, 0) || b.build(Gun, 0, 0) {
		t.Fatal("tile should take exactly one tower")
	}
	b.Money = 0
	if b.build(Gun, 1, 0) {
		t.Fatal("built without money")
	}
	b.Money = 10000
	tw := b.towerAt(0, 0)
	for b.upgrade(tw) {
	}
	if tw.Lvl != maxLevel {
		t.Fatalf("lvl %d", tw.Lvl)
	}
	before, value := b.Money, sellValue(tw)
	b.sell(tw)
	if b.Money != before+value || b.towerAt(0, 0) != nil {
		t.Fatal("sell")
	}
}

func TestSendRaisesPayAndSpawnsOnRival(t *testing.T) {
	g := newGame(1)
	g.ai.next = math.Inf(1)
	if g.send(0, g.You) {
		t.Fatal("sends are locked before round 1")
	}
	sim(g, firstRound+0.1)
	m, inc := g.You.Money, g.You.Income
	if !g.send(0, g.You) {
		t.Fatal("send failed")
	}
	if g.You.Money != m-sends[0].cost || g.You.Income != inc+sends[0].income {
		t.Fatal("send did not cost money / raise pay")
	}
	rivalBefore := len(g.Rival.queue) + len(g.Rival.Enemies)
	if rivalBefore < sends[0].count {
		t.Fatal("sent bots missing on rival board")
	}
}

func TestFrostSlows(t *testing.T) {
	b := newBoard()
	b.build(Frost, 4, 2)
	g := &Game{You: b, Rival: newBoard(), ai: &AI{next: math.Inf(1)}, nextRound: math.Inf(1), nextPay: math.Inf(1)}
	e := &Enemy{Kind: Scrap, HP: 100, MaxHP: 100, Dist: 4} // at (3,1), in range
	b.Enemies = []*Enemy{e}
	g.step(b)
	if e.Slow <= 0 || e.Dist >= 4+enemySpecs[Scrap].speed*dt {
		t.Fatalf("not slowed: slow %v dist %v", e.Slow, e.Dist)
	}
}

func TestRivalBuildsAndCanLose(t *testing.T) {
	g := newGame(2)
	sim(g, 120)
	if len(g.Rival.Towers) < 2 {
		t.Fatalf("rival built %d towers", len(g.Rival.Towers))
	}
	for _, tw := range g.Rival.Towers {
		if !buildable(tw.X, tw.Y) {
			t.Fatalf("rival tower on path at %d,%d", tw.X, tw.Y)
		}
	}
	g.Over, g.You.Lives, g.Rival.Lives = "", startLives, 0
	g.update()
	if g.Over != "win" {
		t.Fatalf("over = %q", g.Over)
	}
}

func TestParseInput(t *testing.T) {
	ev := parseInput([]byte("a\x1b[A\x1b[1;2C\x1b[<0;5;7M\x1b[<35;6;7M\x1b[<0;6;7m\x03\x1b_Gi=1;OK\x1b\\é"))
	var got []string
	for _, e := range ev {
		got = append(got, e.key)
	}
	want := "a up S-right mouse mouse mouse ctrl-c é"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q", strings.Join(got, " "))
	}
	if ev[3].mx != 4 || ev[3].my != 6 || !ev[3].press || !ev[4].mov || ev[4].btn != 3 || ev[5].press {
		t.Fatalf("mouse %+v %+v %+v", ev[3], ev[4], ev[5])
	}
}

func TestHookAndInbox(t *testing.T) {
	t.Setenv("FACTORY_HOME", t.TempDir())
	cmdHook(strings.NewReader(`{"hook_event_name":"Stop","cwd":"/x/proj","last_assistant_message":"## Done\n- tests pass"}`))
	cmdHook(strings.NewReader(`{"hook_event_name":"Notification","cwd":"/x/proj","message":"approve?"}`))
	m := readInbox()
	if len(m) != 2 || m[0].From != "proj" || !strings.Contains(m[0].Text, "tests pass") || !strings.HasPrefix(m[1].Text, "needs you") {
		t.Fatalf("%+v", m)
	}
	if len(readInbox()) != 0 {
		t.Fatal("inbox not drained")
	}
}

// TestRenderPNG renders a mid-match frame; set FACTORY_PNG=path to look at it.
func TestRenderPNG(t *testing.T) {
	g := newGame(3)
	g.You.Money = 100000
	for i, xy := range [][2]int{{5, 2}, {9, 2}, {12, 3}, {6, 5}, {4, 7}, {9, 9}, {15, 9}, {11, 6}} {
		g.You.build(TowerKind(i%4), xy[0], xy[1])
	}
	for _, tw := range g.You.Towers[:4] {
		g.You.upgrade(tw)
	}
	sim(g, firstRound+roundEvery*4+6)
	g.send(2, g.Rival)
	sim(g, 6)
	f, v := newRenderer().draw(g, 1200, 560, [2]int{11, 6}, int(Cannon), 1.3, true)
	if v.T == 0 || v.RivalT == 0 {
		t.Fatalf("view %+v", v)
	}
	if out := os.Getenv("FACTORY_PNG"); out != "" {
		img := image.NewRGBA(image.Rect(0, 0, f.w, f.h))
		for i := 0; i < f.w*f.h; i++ {
			copy(img.Pix[i*4:], f.px[i*3:i*3+3])
			img.Pix[i*4+3] = 255
		}
		fh, _ := os.Create(out)
		png.Encode(fh, img)
		fh.Close()
	}
}
