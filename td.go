package main

import (
	"math"
	"math/rand"
)

// --- content ---

type EnemyKind uint8

const (
	Scrap EnemyKind = iota
	Bolt
	Brute
	Hulk
	numEnemy
)

type enemySpec struct {
	name      string
	hp, speed float64 // speed in tiles per second
	bounty    int
	leak      int // lives lost when it reaches the factory
	size      float64
}

var enemySpecs = [numEnemy]enemySpec{
	{"scrap bot", 1, 1.6, 2, 1, 0.42},
	{"bolt", 3, 2.8, 4, 2, 0.4},
	{"brute", 14, 1.0, 12, 6, 0.58},
	{"hulk", 60, 0.65, 40, 20, 0.72},
}

type TowerKind uint8

const (
	Gun TowerKind = iota
	Cannon
	Tesla
	Frost
	numTower
)

type towerSpec struct {
	name   string
	cost   int
	rng    float64 // tiles
	dmg    float64
	rate   float64 // shots per second
	splash float64 // cannon blast radius
	chain  int     // tesla jumps
	slow   float64 // frost: speed multiplier on bots in range
	blurb  string
}

var towerSpecs = [numTower]towerSpec{
	{"Gun turret", 150, 2.6, 1, 2.2, 0, 0, 0, "fast single shots"},
	{"Cannon", 320, 3.0, 2, 0.8, 0.9, 0, 0, "blasts groups"},
	{"Tesla coil", 450, 2.3, 1.5, 1.5, 0, 3, 0, "arcs through 3 bots"},
	{"Frost vent", 220, 1.8, 0, 1.2, 0, 0, 0.5, "slows bots in range"},
}

const maxLevel = 4

type sendSpec struct {
	key      string
	kind     EnemyKind
	count    int
	cost     int
	income   int // added to the sender's pay every payday
	minRound int
}

var sends = []sendSpec{
	{"z", Scrap, 6, 30, 2, 1},
	{"c", Bolt, 5, 70, 4, 2},
	{"v", Brute, 3, 150, 8, 4},
	{"b", Hulk, 1, 300, 15, 7},
}

const (
	tps          = 20
	dt           = 1.0 / tps
	boardW       = 18
	boardH       = 12
	startMoney   = 500
	startIncome  = 120
	startLives   = 100
	payEvery     = 6.0  // seconds
	roundEvery   = 20.0 // seconds
	firstRound   = 4.0
	sellRefund   = 0.75
	spawnSpacing = 0.45
)

// path waypoints (tile centers); bots enter off the left edge, the factory sits at the end.
var waypoints = [][2]float64{{-1, 1}, {14, 1}, {14, 4}, {3, 4}, {3, 8}, {13, 8}, {13, 10}, {16, 10}}

// --- state ---

type Enemy struct {
	Kind      EnemyKind
	Dist      float64
	HP, MaxHP float64
	Slow      float64 // seconds left slowed
}

type Tower struct {
	Kind  TowerKind
	X, Y  int
	Lvl   int
	Spent int
	cd    float64
	Aim   float64 // radians, for drawing
	Flash float64 // seconds left of muzzle/effect flash
}

type FX struct {
	kind           int // 0 tracer, 1 blast, 2 arc, 3 frost pulse
	x0, y0, x1, y1 float64
	r              float64
	t, life        float64
}

type spawn struct {
	kind EnemyKind
	at   float64
}

type Board struct {
	Lives   int
	Money   int
	Income  int
	Towers  []*Tower
	Enemies []*Enemy
	queue   []spawn
	FX      []FX
	Sent    int // bots sent to the other side
	Kills   int
}

type Game struct {
	You, Rival *Board
	Time       float64
	Round      int
	nextRound  float64
	nextPay    float64
	Over       string // "", "win", "lose"
	ai         *AI
	rng        *rand.Rand
	onEvent    func(string)
}

func newBoard() *Board {
	return &Board{Lives: startLives, Money: startMoney, Income: startIncome}
}

func newGame(seed int64) *Game {
	r := rand.New(rand.NewSource(seed))
	g := &Game{You: newBoard(), Rival: newBoard(), nextRound: firstRound, nextPay: payEvery, rng: r}
	g.ai = &AI{rng: r}
	return g
}

func (g *Game) event(s string) {
	if g.onEvent != nil {
		g.onEvent(s)
	}
}

// --- path ---

var pathTiles = func() map[[2]int]bool {
	m := map[[2]int]bool{}
	for _, t := range pathOrder() {
		m[t] = true
	}
	return m
}()

// pathOrder lists on-board path tiles from entry to factory.
func pathOrder() [][2]int {
	var out [][2]int
	seen := map[[2]int]bool{}
	for i := 0; i+1 < len(waypoints); i++ {
		a, b := waypoints[i], waypoints[i+1]
		sx, sy := sign(b[0]-a[0]), sign(b[1]-a[1])
		x, y := int(a[0]), int(a[1])
		for {
			t := [2]int{x, y}
			if x >= 0 && y >= 0 && x < boardW && y < boardH && !seen[t] {
				seen[t] = true
				out = append(out, t)
			}
			if x == int(b[0]) && y == int(b[1]) {
				break
			}
			x, y = x+sx, y+sy
		}
	}
	return out
}

func sign(v float64) int {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

var pathLen = func() float64 {
	l := 0.0
	for i := 0; i+1 < len(waypoints); i++ {
		l += math.Abs(waypoints[i+1][0]-waypoints[i][0]) + math.Abs(waypoints[i+1][1]-waypoints[i][1])
	}
	return l
}()

// pos returns the tile-space position at distance d along the path.
func pos(d float64) (float64, float64) {
	for i := 0; i+1 < len(waypoints); i++ {
		a, b := waypoints[i], waypoints[i+1]
		l := math.Abs(b[0]-a[0]) + math.Abs(b[1]-a[1])
		if d <= l {
			t := d / l
			return a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t
		}
		d -= l
	}
	w := waypoints[len(waypoints)-1]
	return w[0], w[1]
}

// factory tile: the end of the path.
var factoryTile = [2]int{int(waypoints[len(waypoints)-1][0]), int(waypoints[len(waypoints)-1][1])}

func buildable(x, y int) bool {
	t := [2]int{x, y}
	return x >= 0 && y >= 0 && x < boardW && y < boardH && !pathTiles[t] && t != factoryTile
}

// --- towers ---

func (t *Tower) stats() towerSpec {
	s := towerSpecs[t.Kind]
	l := float64(t.Lvl - 1)
	s.dmg *= 1 + 0.7*l
	s.rng *= 1 + 0.12*l
	s.rate *= 1 + 0.25*l
	s.splash *= 1 + 0.15*l
	s.chain += t.Lvl - 1
	if s.slow > 0 {
		s.slow = math.Max(0.2, s.slow-0.08*l)
		if t.Lvl >= 3 {
			s.dmg = 0.5 * l // top frost vents also chip damage
		}
	}
	return s
}

func upgradeCost(t *Tower) int { return towerSpecs[t.Kind].cost * (t.Lvl + 1) * 3 / 5 }
func sellValue(t *Tower) int   { return int(float64(t.Spent) * sellRefund) }

func (b *Board) towerAt(x, y int) *Tower {
	for _, t := range b.Towers {
		if t.X == x && t.Y == y {
			return t
		}
	}
	return nil
}

func (b *Board) build(k TowerKind, x, y int) bool {
	c := towerSpecs[k].cost
	if !buildable(x, y) || b.towerAt(x, y) != nil || b.Money < c {
		return false
	}
	b.Money -= c
	b.Towers = append(b.Towers, &Tower{Kind: k, X: x, Y: y, Lvl: 1, Spent: c, Aim: -math.Pi / 2})
	return true
}

func (b *Board) upgrade(t *Tower) bool {
	if t == nil || t.Lvl >= maxLevel || b.Money < upgradeCost(t) {
		return false
	}
	c := upgradeCost(t)
	b.Money -= c
	t.Spent += c
	t.Lvl++
	return true
}

func (b *Board) sell(t *Tower) {
	for i, o := range b.Towers {
		if o == t {
			b.Money += sellValue(t)
			b.Towers = append(b.Towers[:i], b.Towers[i+1:]...)
			return
		}
	}
}

// --- sending ---

func (g *Game) other(b *Board) *Board {
	if b == g.You {
		return g.Rival
	}
	return g.You
}

func (g *Game) canSend(s sendSpec, from *Board) bool {
	return g.Over == "" && g.Round >= s.minRound && from.Money >= s.cost
}

// send pays for a group of bots on the other board and raises the sender's pay.
func (g *Game) send(i int, from *Board) bool {
	s := sends[i]
	if !g.canSend(s, from) {
		return false
	}
	from.Money -= s.cost
	from.Income += s.income
	from.Sent += s.count
	to := g.other(from)
	start := g.Time
	if n := len(to.queue); n > 0 {
		start = math.Max(start, to.queue[n-1].at)
	}
	for k := 0; k < s.count; k++ {
		to.queue = append(to.queue, spawn{s.kind, start + float64(k+1)*spawnSpacing*0.7})
	}
	return true
}

// wave returns round n's bots, the same on both boards.
func wave(n int) []EnemyKind {
	var w []EnemyKind
	add := func(k EnemyKind, c int) {
		for i := 0; i < c; i++ {
			w = append(w, k)
		}
	}
	add(Scrap, 5+2*n)
	if n >= 3 {
		add(Bolt, n)
	}
	if n >= 5 {
		add(Brute, (n-3)/2)
	}
	if n >= 9 {
		add(Hulk, (n-7)/2)
	}
	return w
}

// --- simulation ---

func (g *Game) update() {
	if g.Over != "" {
		return
	}
	g.Time += dt
	if g.Time >= g.nextPay {
		g.nextPay += payEvery
		g.You.Money += g.You.Income
		g.Rival.Money += g.Rival.Income
	}
	if g.Time >= g.nextRound {
		g.Round++
		g.nextRound += roundEvery
		for _, b := range []*Board{g.You, g.Rival} {
			start := math.Max(g.Time, lastSpawn(b))
			for i, k := range wave(g.Round) {
				b.queue = append(b.queue, spawn{k, start + float64(i)*spawnSpacing})
			}
		}
		if g.Round == 1 {
			g.event("Round 1: the bots are coming. Build turrets next to the conveyor!")
		}
		for _, s := range sends {
			if s.minRound == g.Round && g.Round > 1 {
				g.event("New send unlocked: " + enemySpecs[s.kind].name + "s (" + s.key + ")")
			}
		}
	}
	g.ai.think(g)
	for _, b := range []*Board{g.You, g.Rival} {
		g.step(b)
	}
	switch {
	case g.You.Lives <= 0:
		g.Over = "lose"
	case g.Rival.Lives <= 0:
		g.Over = "win"
	}
}

func lastSpawn(b *Board) float64 {
	if n := len(b.queue); n > 0 {
		return b.queue[n-1].at
	}
	return 0
}

func (g *Game) step(b *Board) {
	// spawn
	for len(b.queue) > 0 && b.queue[0].at <= g.Time {
		k := b.queue[0].kind
		hp := enemySpecs[k].hp * (1 + 0.06*float64(max(0, g.Round-1))) // bots toughen slowly
		b.Enemies = append(b.Enemies, &Enemy{Kind: k, HP: hp, MaxHP: hp})
		b.queue = b.queue[1:]
	}
	// frost first, so slows apply this tick
	for _, e := range b.Enemies {
		e.Slow = math.Max(0, e.Slow-dt)
	}
	for _, t := range b.Towers {
		s := t.stats()
		t.Flash = math.Max(0, t.Flash-dt)
		if s.slow == 0 {
			continue
		}
		for _, e := range b.Enemies {
			if inRange(t, e, s.rng) {
				e.Slow = 0.25
			}
		}
	}
	// move
	alive := b.Enemies[:0]
	for _, e := range b.Enemies {
		sp := enemySpecs[e.Kind].speed
		if e.Slow > 0 {
			sp *= slowFactor(b, e)
		}
		e.Dist += sp * dt
		if e.Dist >= pathLen {
			b.Lives -= enemySpecs[e.Kind].leak
			continue
		}
		alive = append(alive, e)
	}
	b.Enemies = alive
	// shoot
	for _, t := range b.Towers {
		s := t.stats()
		t.cd -= dt
		if t.cd > 0 {
			continue
		}
		target := first(b, t, s.rng)
		if target == nil {
			continue
		}
		t.cd = 1 / s.rate
		t.Flash = 0.12
		tx, ty := pos(target.Dist)
		cx, cy := float64(t.X), float64(t.Y)
		if s.slow == 0 {
			t.Aim = math.Atan2(ty-cy, tx-cx)
		}
		switch t.Kind {
		case Gun:
			b.FX = append(b.FX, FX{kind: 0, x0: cx, y0: cy, x1: tx, y1: ty, life: 0.08})
			b.hit(target, s.dmg)
		case Cannon:
			b.FX = append(b.FX, FX{kind: 1, x0: tx, y0: ty, r: s.splash, life: 0.25})
			for _, e := range b.Enemies {
				ex, ey := pos(e.Dist)
				if math.Hypot(ex-tx, ey-ty) <= s.splash {
					b.hit(e, s.dmg)
				}
			}
		case Tesla:
			hitSet := map[*Enemy]bool{}
			px, py := cx, cy
			cur := target
			for j := 0; j < s.chain && cur != nil; j++ {
				ex, ey := pos(cur.Dist)
				b.FX = append(b.FX, FX{kind: 2, x0: px, y0: py, x1: ex, y1: ey, life: 0.15})
				hitSet[cur] = true
				b.hit(cur, s.dmg)
				px, py = ex, ey
				cur = nearest(b, px, py, 1.6, hitSet)
			}
		case Frost:
			b.FX = append(b.FX, FX{kind: 3, x0: cx, y0: cy, r: s.rng, life: 0.4})
			if s.dmg > 0 {
				for _, e := range b.Enemies {
					if inRange(t, e, s.rng) {
						b.hit(e, s.dmg)
					}
				}
			}
		}
	}
	// clear dead, age effects
	alive = b.Enemies[:0]
	for _, e := range b.Enemies {
		if e.HP > 0 {
			alive = append(alive, e)
		}
	}
	b.Enemies = alive
	fx := b.FX[:0]
	for _, f := range b.FX {
		if f.t += dt; f.t < f.life {
			fx = append(fx, f)
		}
	}
	b.FX = fx
}

// slowFactor is the strongest frost covering e.
func slowFactor(b *Board, e *Enemy) float64 {
	f := 1.0
	for _, t := range b.Towers {
		if s := t.stats(); s.slow > 0 && inRange(t, e, s.rng) {
			f = math.Min(f, s.slow)
		}
	}
	return f
}

func (b *Board) hit(e *Enemy, d float64) {
	if e.HP <= 0 || d <= 0 {
		return
	}
	e.HP -= d
	if e.HP <= 0 {
		b.Money += enemySpecs[e.Kind].bounty
		b.Kills++
	}
}

func inRange(t *Tower, e *Enemy, r float64) bool {
	x, y := pos(e.Dist)
	return x >= -0.5 && math.Hypot(x-float64(t.X), y-float64(t.Y)) <= r
}

// first is the bot in range that is furthest along the path.
func first(b *Board, t *Tower, r float64) *Enemy {
	var best *Enemy
	for _, e := range b.Enemies {
		if e.HP > 0 && inRange(t, e, r) && (best == nil || e.Dist > best.Dist) {
			best = e
		}
	}
	return best
}

func nearest(b *Board, x, y, r float64, skip map[*Enemy]bool) *Enemy {
	var best *Enemy
	bd := r
	for _, e := range b.Enemies {
		ex, ey := pos(e.Dist)
		if d := math.Hypot(ex-x, ey-y); e.HP > 0 && !skip[e] && d <= bd {
			best, bd = e, d
		}
	}
	return best
}

// --- rival AI ---

type AI struct {
	rng  *rand.Rand
	next float64
	n    int
}

var aiOrder = []TowerKind{Gun, Gun, Frost, Cannon, Gun, Tesla, Cannon, Tesla, Frost, Cannon}

// coverage counts path tiles within r of x,y.
func coverage(x, y int, r float64) int {
	n := 0
	for t := range pathTiles {
		if math.Hypot(float64(t[0]-x), float64(t[1]-y)) <= r {
			n++
		}
	}
	return n
}

func (a *AI) think(g *Game) {
	if g.Time < a.next {
		return
	}
	a.next = g.Time + 1.5 + a.rng.Float64()
	b := g.Rival
	want := 2 + g.Round*2/3
	if len(b.Towers) < want {
		k := aiOrder[a.n%len(aiOrder)]
		if b.Money < towerSpecs[k].cost {
			return // save up
		}
		bx, by, best := -1, -1, -1
		for y := 0; y < boardH; y++ {
			for x := 0; x < boardW; x++ {
				if !buildable(x, y) || b.towerAt(x, y) != nil {
					continue
				}
				if c := coverage(x, y, towerSpecs[k].rng)*10 + a.rng.Intn(8); c > best {
					bx, by, best = x, y, c
				}
			}
		}
		if bx >= 0 && b.build(k, bx, by) {
			a.n++
		}
		return
	}
	// upgrade the cheapest-to-upgrade tower, keeping some cash to send
	var pick *Tower
	for _, t := range b.Towers {
		if t.Lvl < maxLevel && (pick == nil || upgradeCost(t) < upgradeCost(pick)) {
			pick = t
		}
	}
	if pick != nil && b.Money >= upgradeCost(pick)+60 && a.rng.Float64() < 0.6 {
		b.upgrade(pick)
		return
	}
	for i := len(sends) - 1; i >= 0; i-- {
		if s := sends[i]; g.canSend(s, b) && b.Money >= s.cost*2 {
			g.send(i, b)
			return
		}
	}
}
