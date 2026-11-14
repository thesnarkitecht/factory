package main

import (
	"image"
	"image/png"
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

// playRound starts the next round and runs until it is cleared or lost.
func playRound(g *Game) {
	g.startRound()
	for i := 0; i < tps*300 && g.Active && g.Over != "lose"; i++ {
		g.update()
	}
}

func TestUndefendedLeaks(t *testing.T) {
	g := newGame()
	playRound(g)
	if g.Lives >= startLives || g.Active {
		t.Fatalf("lives %d active %v", g.Lives, g.Active)
	}
}

func TestRoundsPayBonusAndWait(t *testing.T) {
	g := newGame()
	g.Money = 10000
	for _, xy := range [][2]int{{5, 2}, {8, 2}, {11, 2}, {6, 5}} {
		if !g.build(Gun, xy[0], xy[1]) {
			t.Fatalf("build at %v failed", xy)
		}
	}
	m := g.Money
	sim(g, 5)
	if g.Round != 0 || len(g.Enemies) != 0 {
		t.Fatal("rounds must wait for the player")
	}
	playRound(g)
	if g.Lives != startLives || g.Kills != len(wave(1)) {
		t.Fatalf("lives %d kills %d", g.Lives, g.Kills)
	}
	if g.Money != m+len(wave(1))*enemySpecs[Scrap].bounty+roundBonus(1) {
		t.Fatalf("money %d", g.Money)
	}
	g.Auto = true
	playRound(g)
	if g.Round < 3 || !g.Active {
		t.Fatalf("auto-start: round %d active %v", g.Round, g.Active)
	}
}

func TestUpgradePaths(t *testing.T) {
	g := newGame()
	g.Money = 100000
	g.build(Gun, 0, 0)
	tw := g.towerAt(0, 0)
	for i := 0; i < 4; i++ {
		if !g.upgrade(tw, 0) {
			t.Fatalf("top path tier %d failed", i+1)
		}
	}
	if g.upgrade(tw, 0) {
		t.Fatal("went past tier 4")
	}
	if !g.upgrade(tw, 1) || !g.upgrade(tw, 1) || g.upgrade(tw, 1) {
		t.Fatal("bottom path should stop at tier 2 once top passes it")
	}
	s := tw.stats()
	if s.shots != 3 || !s.pierce || s.rate <= towerSpecs[Gun].base.rate*4 {
		t.Fatalf("stats %+v", s)
	}
	before, value := g.Money, sellValue(tw)
	g.sell(tw)
	if g.Money != before+value || g.towerAt(0, 0) != nil {
		t.Fatal("sell")
	}
}

func TestBuildRules(t *testing.T) {
	g := newGame()
	if g.build(Gun, 3, 1) {
		t.Fatal("built on the path")
	}
	if !g.build(Gun, 0, 0) || g.build(Gun, 0, 0) {
		t.Fatal("tile should take exactly one tower")
	}
	g.Money = 0
	if g.build(Gun, 1, 0) {
		t.Fatal("built without money")
	}
}

func TestArmorNeedsPierce(t *testing.T) {
	g := newGame()
	g.Money = 100000
	g.build(Gun, 4, 2)
	tw := g.towerAt(4, 2)
	e := &Enemy{Kind: Hulk, HP: 50, MaxHP: 50, Dist: 4}
	g.Enemies = []*Enemy{e}
	g.fire(tw)
	if e.HP != 50 {
		t.Fatal("plain bullets hurt an armored hulk")
	}
	g.upgrade(tw, 1)
	g.upgrade(tw, 1) // AP Rounds
	tw.cd = 0
	g.fire(tw)
	if e.HP >= 50 {
		t.Fatal("AP rounds should pierce armor")
	}
}

func TestFrostSlows(t *testing.T) {
	g := newGame()
	g.build(Frost, 4, 2)
	e := &Enemy{Kind: Scrap, HP: 100, MaxHP: 100, Dist: 4} // at (3,1), in range
	g.Enemies = []*Enemy{e}
	g.step()
	if e.Slow <= 0 || e.Dist >= 4+enemySpecs[Scrap].speed*dt {
		t.Fatalf("not slowed: slow %v dist %v", e.Slow, e.Dist)
	}
}

func TestLoseAndWin(t *testing.T) {
	g := newGame()
	g.Lives = 1
	playRound(g)
	if g.Over != "lose" || g.build(Gun, 0, 0) {
		t.Fatalf("over %q", g.Over)
	}
	g = newGame()
	g.Round = winRound - 1
	g.startRound()
	g.queue, g.Enemies = nil, nil
	g.update()
	if g.Over != "win" || g.Active {
		t.Fatalf("over %q", g.Over)
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
	g := newGame()
	g.Money = 1000000
	for i, xy := range [][2]int{{5, 2}, {9, 2}, {12, 3}, {6, 5}, {4, 7}, {9, 9}, {15, 9}, {11, 6}} {
		g.build(TowerKind(i%4), xy[0], xy[1])
	}
	for i, tw := range g.Towers[:6] {
		for j := 0; j <= i%5; j++ {
			g.upgrade(tw, i%2)
		}
	}
	g.Round = 13
	g.startRound()
	sim(g, 9)
	f, T := newRenderer().draw(g, 1000, 660, [2]int{11, 6}, int(Cannon), 1.3)
	if T == 0 {
		t.Fatal("no tiles")
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
