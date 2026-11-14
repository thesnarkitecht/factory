package main

import (
	"encoding/json"
	"math/rand"
	"os"
	"time"
)

type Item uint8

const (
	None Item = iota
	CopperOre
	IronOre
	Coal
	Stone
	CopperPlate
	IronPlate
	Brick
	Gear
	Wire
	Circuit
	Engine
	numItems
)

var itemName = [numItems]string{"", "copper ore", "iron ore", "coal", "stone", "copper plate", "iron plate", "brick", "gear", "wire", "circuit", "engine"}
var itemPrice = [numItems]int{0, 1, 1, 1, 1, 4, 4, 6, 12, 3, 30, 180}

type Kind uint8

const (
	Empty Kind = iota
	Belt
	Drill
	Furnace
	Assembler
	Trade
	numKinds
)

var kindName = [numKinds]string{"", "Belt", "Drill", "Furnace", "Assembler", "Trade post"}
var kindCost = [numKinds]int{0, 1, 25, 40, 100, 60}
var kindUnlock = [numKinds]int{0, 0, 0, 0, 300, 0} // total earned needed

type stack struct {
	item Item
	n    int
}

type Recipe struct {
	name   string
	in     []stack
	out    Item
	outN   int
	secs   float64
	unlock int
}

var furnaceRecipes = []Recipe{
	{"copper plate", []stack{{CopperOre, 1}}, CopperPlate, 1, 2, 0},
	{"iron plate", []stack{{IronOre, 1}}, IronPlate, 1, 2, 0},
	{"brick", []stack{{Stone, 2}}, Brick, 1, 2, 0},
}

var asmRecipes = []Recipe{
	{"gear", []stack{{IronPlate, 2}}, Gear, 1, 1.5, 0},
	{"wire", []stack{{CopperPlate, 1}}, Wire, 2, 1, 0},
	{"circuit", []stack{{IronPlate, 1}, {Wire, 3}}, Circuit, 1, 3, 2000},
	{"engine", []stack{{Gear, 2}, {Circuit, 1}, {Brick, 2}}, Engine, 1, 5, 8000},
}

const (
	goal       = 50000
	tps        = 20
	dt         = 1.0 / tps
	beltSpeed  = 2.0 // tiles per second
	drillSecs  = 1.5
	smeltsCoal = 4 // smelts per coal
	outCap     = 4
)

// Dirs: 0 up, 1 right, 2 down, 3 left.
var dx = [4]int{0, 1, 0, -1}
var dy = [4]int{-1, 0, 1, 0}

type Building struct {
	Kind   Kind
	Dir    int
	Recipe int     // assembler recipe
	Item   Item    // belt: carried item
	Prog   float64 // belt: item progress 0..1; machine: craft progress
	Inv    [numItems]int
	Out    []Item
	Fuel   int
	Active int    // recipe in progress, -1 none
	Status string `json:"-"`
}

type Game struct {
	W, H    int
	Seed    int64
	Money   int
	Earned  int
	Sold    [numItems]int
	Won     bool
	ore     []Item
	b       []*Building
	income  [60]int // dollars per second, ring
	incIdx  int
	tick    int
	onEvent func(string)
}

func newGame(seed int64) *Game {
	g := &Game{W: 160, H: 120, Seed: seed, Money: 250}
	g.genOre()
	g.b = make([]*Building, g.W*g.H)
	return g
}

func (g *Game) genOre() {
	g.ore = make([]Item, g.W*g.H)
	r := rand.New(rand.NewSource(g.Seed))
	blob := func(cx, cy int, rad float64, it Item) {
		for y := cy - int(rad) - 2; y <= cy+int(rad)+2; y++ {
			for x := cx - int(rad) - 2; x <= cx+int(rad)+2; x++ {
				if !g.in(x, y) {
					continue
				}
				ddx, ddy := float64(x-cx), float64(y-cy)
				if ddx*ddx+ddy*ddy < rad*rad*(0.7+0.5*r.Float64()) {
					g.ore[y*g.W+x] = it
				}
			}
		}
	}
	// guaranteed starter patches around the center
	cx, cy := g.W/2, g.H/2
	blob(cx-8, cy-5, 3.5, CopperOre)
	blob(cx+8, cy-5, 3.5, IronOre)
	blob(cx-8, cy+6, 3, Coal)
	blob(cx+8, cy+6, 3, Stone)
	for i := 0; i < 70; i++ {
		x, y := r.Intn(g.W), r.Intn(g.H)
		if abs(x-cx) < 14 && abs(y-cy) < 11 {
			continue
		}
		blob(x, y, 2+r.Float64()*4, Item(1+r.Intn(4)))
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

func (g *Game) in(x, y int) bool { return x >= 0 && y >= 0 && x < g.W && y < g.H }

func (g *Game) at(x, y int) *Building {
	if !g.in(x, y) {
		return nil
	}
	return g.b[y*g.W+x]
}

func (g *Game) oreAt(x, y int) Item {
	if !g.in(x, y) {
		return None
	}
	return g.ore[y*g.W+x]
}

func (g *Game) unlocked(k Kind) bool { return g.Earned >= kindUnlock[k] }

// place builds kind k at x,y facing dir; replacing another kind refunds it.
func (g *Game) place(x, y int, k Kind, dir int) bool {
	if !g.in(x, y) || !g.unlocked(k) {
		return false
	}
	if old := g.at(x, y); old != nil {
		if old.Kind == k {
			old.Dir = dir
			return true
		}
		if g.Money+kindCost[old.Kind] < kindCost[k] {
			return false
		}
		g.remove(x, y)
	}
	if g.Money < kindCost[k] {
		return false
	}
	g.Money -= kindCost[k]
	g.b[y*g.W+x] = &Building{Kind: k, Dir: dir, Active: -1}
	return true
}

func (g *Game) remove(x, y int) {
	if b := g.at(x, y); b != nil {
		g.Money += kindCost[b.Kind] // cozy: full refund
		g.b[y*g.W+x] = nil
	}
}

func (g *Game) recipesFor(b *Building) []Recipe {
	switch b.Kind {
	case Furnace:
		return furnaceRecipes
	case Assembler:
		return asmRecipes[b.Recipe : b.Recipe+1]
	}
	return nil
}

func uses(r Recipe, it Item) int {
	for _, s := range r.in {
		if s.item == it {
			return s.n
		}
	}
	return 0
}

// accept offers item to the building at x,y, travelling in dir.
func (g *Game) accept(x, y int, it Item, dir int) bool {
	b := g.at(x, y)
	if b == nil {
		return false
	}
	switch b.Kind {
	case Belt:
		if b.Item == None && b.Dir != (dir+2)%4 {
			b.Item, b.Prog = it, 0
			return true
		}
	case Furnace:
		if it == Coal {
			if b.Inv[Coal] < 8 {
				b.Inv[Coal]++
				return true
			}
			return false
		}
		fallthrough
	case Assembler:
		for _, r := range g.recipesFor(b) {
			if n := uses(r, it); n > 0 && b.Inv[it] < 2*n+2 {
				b.Inv[it]++
				return true
			}
		}
	case Trade:
		g.sell(it)
		return true
	}
	return false
}

func (g *Game) sell(it Item) {
	p := itemPrice[it]
	g.Money += p
	g.Earned += p
	g.Sold[it]++
	g.income[g.incIdx] += p
	if !g.Won && g.Earned >= goal {
		g.Won = true
		g.event("You reached $50,000 earned. The factory is yours. Keep building!")
	}
}

func (g *Game) event(s string) {
	if g.onEvent != nil {
		g.onEvent(s)
	}
}

// rate is dollars per second over the last minute.
func (g *Game) rate() float64 {
	s := 0
	for _, v := range g.income {
		s += v
	}
	return float64(s) / 60
}

func (g *Game) update() {
	g.tick++
	if g.tick%tps == 0 {
		g.incIdx = (g.incIdx + 1) % len(g.income)
		g.income[g.incIdx] = 0
	}
	for i, b := range g.b {
		if b == nil {
			continue
		}
		x, y := i%g.W, i/g.W
		fx, fy := x+dx[b.Dir], y+dy[b.Dir]
		switch b.Kind {
		case Belt:
			if b.Item != None {
				b.Prog += dt * beltSpeed
				if b.Prog >= 1 {
					b.Prog = 1
					it := b.Item
					b.Item = None
					if !g.accept(fx, fy, it, b.Dir) {
						b.Item = it
					}
				}
			}
			continue
		case Drill:
			ore := g.oreAt(x, y)
			b.Status = ""
			if ore == None {
				b.Status = "NO ORE"
			} else if len(b.Out) >= outCap {
				b.Status = "FULL"
			} else if b.Prog += dt / drillSecs; b.Prog >= 1 {
				b.Prog = 0
				b.Out = append(b.Out, ore)
			}
		case Furnace, Assembler:
			g.craft(b)
		}
		if len(b.Out) > 0 && g.accept(fx, fy, b.Out[0], b.Dir) {
			b.Out = b.Out[1:]
		}
	}
}

func (g *Game) craft(b *Building) {
	rs := g.recipesFor(b)
	if b.Active < 0 {
		b.Status = "NEED INPUT"
		for i, r := range rs {
			ok := true
			for _, s := range r.in {
				ok = ok && b.Inv[s.item] >= s.n
			}
			if !ok {
				continue
			}
			if b.Kind == Furnace && b.Fuel == 0 {
				if b.Inv[Coal] == 0 {
					b.Status = "NEED COAL"
					return
				}
				b.Inv[Coal]--
				b.Fuel = smeltsCoal
			}
			for _, s := range r.in {
				b.Inv[s.item] -= s.n
			}
			if b.Kind == Furnace {
				b.Fuel--
				b.Active = i
			} else {
				b.Active = 0
			}
			b.Prog = 0
			break
		}
		if b.Active < 0 {
			return
		}
	}
	r := rs[b.Active]
	b.Status = ""
	if b.Prog < 1 {
		b.Prog += dt / r.secs
		return
	}
	if len(b.Out)+r.outN > outCap {
		b.Status = "FULL"
		return
	}
	for i := 0; i < r.outN; i++ {
		b.Out = append(b.Out, r.out)
	}
	b.Active = -1
	b.Prog = 0
}

func (b *Building) working() bool {
	switch b.Kind {
	case Drill, Furnace, Assembler:
		return b.Status == ""
	}
	return false
}

// cycleRecipe moves an assembler to its next unlocked recipe.
func (g *Game) cycleRecipe(b *Building) {
	if b == nil || b.Kind != Assembler {
		return
	}
	for i := 1; i <= len(asmRecipes); i++ {
		n := (b.Recipe + i) % len(asmRecipes)
		if g.Earned >= asmRecipes[n].unlock {
			if n != b.Recipe {
				b.Recipe, b.Active, b.Prog, b.Inv = n, -1, 0, [numItems]int{}
			}
			return
		}
	}
}

// --- saving ---

type placed struct {
	X, Y int
	B    *Building
}

type saveFile struct {
	Version int
	Seed    int64
	Money   int
	Earned  int
	Sold    [numItems]int
	Won     bool
	Rate    float64
	SavedAt int64
	Tiles   []placed
}

func (g *Game) save(path string) error {
	s := saveFile{Version: 1, Seed: g.Seed, Money: g.Money, Earned: g.Earned, Sold: g.Sold, Won: g.Won, Rate: g.rate(), SavedAt: time.Now().Unix()}
	for i, b := range g.b {
		if b != nil {
			s.Tiles = append(s.Tiles, placed{i % g.W, i / g.W, b})
		}
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path) // atomic: never leaves a half-written save
}

// load returns the game and the offline earnings since it was saved.
func load(path string) (*Game, int, time.Duration, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, 0, err
	}
	var s saveFile
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, 0, 0, err
	}
	g := newGame(s.Seed)
	g.Money, g.Earned, g.Sold, g.Won = s.Money, s.Earned, s.Sold, s.Won
	for _, t := range s.Tiles {
		if g.in(t.X, t.Y) && t.B != nil && t.B.Kind > Empty && t.B.Kind < numKinds {
			if t.B.Kind == Assembler && (t.B.Recipe < 0 || t.B.Recipe >= len(asmRecipes)) {
				t.B.Recipe = 0
			}
			g.b[t.Y*g.W+t.X] = t.B
		}
	}
	away := time.Since(time.Unix(s.SavedAt, 0))
	if away > 12*time.Hour {
		away = 12 * time.Hour
	}
	bonus := 0
	if away > time.Minute {
		bonus = int(s.Rate * away.Seconds())
		g.Money += bonus
		g.Earned += bonus
	}
	return g, bonus, away, nil
}
