package main

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// starter builds drill(copper)+drill(coal) -> furnace -> belt -> trade post.
func starter(t *testing.T) *Game {
	g := newGame(1)
	g.Money = 10000
	for i := range g.ore {
		g.ore[i] = None
	}
	g.ore[10*g.W+10] = CopperOre
	g.ore[9*g.W+11] = Coal
	ok := g.place(10, 10, Drill, 1) && // copper drill -> furnace at (11,10)
		g.place(11, 9, Drill, 2) && // coal drill pointing down into furnace
		g.place(11, 10, Furnace, 1) &&
		g.place(12, 10, Belt, 1) && g.place(13, 10, Belt, 1) &&
		g.place(14, 10, Trade, 0)
	if !ok {
		t.Fatal("place failed")
	}
	return g
}

func TestChainEarns(t *testing.T) {
	g := starter(t)
	for i := 0; i < tps*60; i++ {
		g.update()
	}
	if g.Sold[CopperPlate] < 10 {
		t.Fatalf("sold %d copper plates in a minute, want >=10 (status %q)", g.Sold[CopperPlate], g.at(11, 10).Status)
	}
	if g.Earned <= 0 || g.rate() <= 0 {
		t.Fatal("no income recorded")
	}
}

func TestFurnaceNeedsCoal(t *testing.T) {
	g := starter(t)
	g.remove(11, 9)
	for i := 0; i < tps*10; i++ {
		g.update()
	}
	if s := g.at(11, 10).Status; s != "NEED COAL" {
		t.Fatalf("status %q", s)
	}
}

func TestAssemblerAndUnlocks(t *testing.T) {
	g := newGame(1)
	if g.place(5, 5, Assembler, 1) {
		t.Fatal("assembler should be locked")
	}
	g.Earned = 300
	if !g.place(5, 5, Assembler, 1) {
		t.Fatal("assembler should unlock at 300")
	}
	b := g.at(5, 5)
	g.cycleRecipe(b)
	if b.Recipe != 1 { // wire
		t.Fatalf("recipe %d", b.Recipe)
	}
	g.cycleRecipe(b) // circuit locked -> back to gear
	if b.Recipe != 0 {
		t.Fatalf("recipe %d, circuit should be locked", b.Recipe)
	}
	for i := 0; i < 2; i++ {
		g.accept(5, 5, IronPlate, 1)
	}
	for i := 0; i < tps*2; i++ {
		g.update()
	}
	if len(b.Out) != 1 || b.Out[0] != Gear {
		t.Fatalf("out %v", b.Out)
	}
}

func TestRefundAndMoney(t *testing.T) {
	g := newGame(1)
	m := g.Money
	g.place(1, 1, Drill, 0)
	g.remove(1, 1)
	if g.Money != m {
		t.Fatalf("money %d want %d", g.Money, m)
	}
	g.Money = 0
	if g.place(1, 1, Drill, 0) {
		t.Fatal("placed without money")
	}
}

func TestSaveLoad(t *testing.T) {
	g := starter(t)
	for i := 0; i < tps*20; i++ {
		g.update()
	}
	p := filepath.Join(t.TempDir(), "s.json")
	if err := g.save(p); err != nil {
		t.Fatal(err)
	}
	h, _, _, err := load(p)
	if err != nil {
		t.Fatal(err)
	}
	if h.Money != g.Money || h.at(11, 10) == nil || h.at(11, 10).Kind != Furnace {
		t.Fatal("round trip lost state")
	}
}

func TestParseInput(t *testing.T) {
	ev := parseInput([]byte("a\x1b[A\x1b[1;2C\x1b[<0;5;7M\x1b[<32;6;7M\x1b[<0;6;7m\x03\x1b_Gi=1;OK\x1b\\é"))
	var got []string
	for _, e := range ev {
		got = append(got, e.key)
	}
	want := "a up S-right mouse mouse mouse ctrl-c é"
	if strings.Join(got, " ") != want {
		t.Fatalf("got %q", strings.Join(got, " "))
	}
	if ev[3].mx != 4 || ev[3].my != 6 || !ev[3].press || ev[4].mov != true || ev[5].press {
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

// TestRenderPNG renders a frame; set FACTORY_PNG=path to look at it.
func TestRenderPNG(t *testing.T) {
	g := starter(t)
	g.Earned = 9000
	g.ore[12*g.W+12], g.ore[12*g.W+13] = IronOre, IronOre
	g.place(12, 12, Drill, 3)
	g.place(10, 12, Assembler, 0)
	for x := 8; x < 15; x++ {
		g.place(x, 14, Belt, 1)
	}
	for i := 0; i < tps*5; i++ {
		g.update()
	}
	for i, it := range []Item{IronOre, Gear, Circuit, Wire, Brick, Engine, Coal} {
		g.at(8+i, 14).Item, g.at(8+i, 14).Prog = it, 0.5
	}
	r := newRenderer()
	r.setTile(40, 40)
	f := r.draw(g, 6, 6, 400, 400, 12, 10, Belt, 1, 0.3)
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
