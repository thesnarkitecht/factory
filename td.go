package main

import (
	"math"
)

// --- content ---

type EnemyKind uint8

const (
	Scrap EnemyKind = iota
	Bolt
	Brute
	Hulk // armored: plain gun bullets bounce off
	numEnemy
)

type enemySpec struct {
	name      string
	hp, speed float64 // speed in tiles per second
	bounty    int
	leak      int // lives lost when it reaches the factory
	size      float64
	armored   bool
}

var enemySpecs = [numEnemy]enemySpec{
	{"scrap bot", 1, 1.8, 1, 1, 0.42, false},
	{"bolt", 3, 3.0, 3, 2, 0.4, false},
	{"brute", 14, 1.1, 10, 6, 0.58, false},
	{"hulk", 50, 0.7, 30, 20, 0.72, true},
}

type TowerKind uint8

const (
	Gun TowerKind = iota
	Cannon
	Tesla
	Frost
	numTower
)

// stats is what a tower does after its upgrades.
type stats struct {
	rng     float64 // tiles
	dmg     float64
	rate    float64 // shots per second
	splash  float64 // blast radius
	chain   int     // tesla jumps
	slow    float64 // speed multiplier on bots in range (frost) or hit (static)
	shots   int     // targets per shot
	pierce  bool    // hurts armored bots
	stun    float64 // seconds bots stop when hit
	brittle float64 // extra damage chilled bots take from everything
	line    bool    // railgun: hits every bot along the line
}

type upgrade struct {
	name string
	cost int
	mod  func(*stats)
}

type towerSpec struct {
	name  string
	cost  int
	base  stats
	blurb string
	paths [2][4]upgrade
}

var towerSpecs = [numTower]towerSpec{
	{"Gun turret", 170, stats{rng: 2.6, dmg: 1, rate: 2.2, shots: 1}, "fast single shots",
		[2][4]upgrade{{
			{"Oiled Gears", 90, func(s *stats) { s.rate *= 1.3 }},
			{"Belt Feed", 200, func(s *stats) { s.rate *= 1.5 }},
			{"Twin Barrels", 550, func(s *stats) { s.shots = 2 }},
			{"Minigun", 2000, func(s *stats) { s.rate *= 2.5; s.shots = 3 }},
		}, {
			{"Long Barrel", 100, func(s *stats) { s.rng *= 1.25 }},
			{"AP Rounds", 260, func(s *stats) { s.dmg++; s.pierce = true }},
			{"Scope", 750, func(s *stats) { s.rng *= 1.4; s.dmg += 2 }},
			{"Railgun", 2600, func(s *stats) { s.dmg += 10; s.line = true; s.rate *= 0.8 }},
		}}},
	{"Cannon", 350, stats{rng: 3.0, dmg: 2, rate: 0.8, splash: 0.9, shots: 1, pierce: true}, "blasts groups, cracks armor",
		[2][4]upgrade{{
			{"Bigger Shells", 160, func(s *stats) { s.splash *= 1.3 }},
			{"Heavy Shells", 320, func(s *stats) { s.dmg++ }},
			{"Cluster Shot", 850, func(s *stats) { s.splash *= 1.5; s.dmg += 2 }},
			{"Demolisher", 3200, func(s *stats) { s.dmg += 8; s.splash *= 1.3 }},
		}, {
			{"Fast Reload", 170, func(s *stats) { s.rate *= 1.35 }},
			{"Long Range", 230, func(s *stats) { s.rng *= 1.3 }},
			{"Stun Shells", 750, func(s *stats) { s.stun = 0.4 }},
			{"Shockwave", 2800, func(s *stats) { s.stun = 1; s.splash *= 1.5 }},
		}}},
	{"Tesla coil", 450, stats{rng: 2.3, dmg: 1.5, rate: 1.5, chain: 3, shots: 1, pierce: true}, "arcs through 3 bots",
		[2][4]upgrade{{
			{"More Coils", 210, func(s *stats) { s.chain++ }},
			{"Chain Storm", 420, func(s *stats) { s.chain += 2 }},
			{"Arc Reactor", 1250, func(s *stats) { s.dmg += 2 }},
			{"Overload", 3600, func(s *stats) { s.chain += 4; s.dmg += 4 }},
		}, {
			{"Capacitor", 190, func(s *stats) { s.rate *= 1.3 }},
			{"Long Arc", 260, func(s *stats) { s.rng *= 1.3 }},
			{"Static Field", 950, func(s *stats) { s.slow = 0.6 }},
			{"Supercell", 3100, func(s *stats) { s.rate *= 2 }},
		}}},
	{"Frost vent", 240, stats{rng: 1.8, rate: 1.2, slow: 0.5, shots: 1, pierce: true}, "slows bots in range",
		[2][4]upgrade{{
			{"Deep Chill", 130, func(s *stats) { s.slow = 0.4 }},
			{"Permafrost", 260, func(s *stats) { s.slow = 0.3 }},
			{"Ice Shards", 720, func(s *stats) { s.dmg = 1 }},
			{"Absolute Zero", 2300, func(s *stats) { s.slow = 0.1; s.dmg = 3 }},
		}, {
			{"Wider Vent", 130, func(s *stats) { s.rng *= 1.25 }},
			{"Brittle", 420, func(s *stats) { s.brittle = 1 }},
			{"Cryo Burst", 620, func(s *stats) { s.rng *= 1.25 }},
			{"Glacier", 2100, func(s *stats) { s.rng *= 1.4; s.brittle = 3 }},
		}}},
}

const (
	tps          = 20
	dt           = 1.0 / tps
	boardW       = 18
	boardH       = 12
	startMoney   = 650
	startLives   = 150
	sellRefund   = 0.75
	spawnSpacing = 0.5
	winRound     = 40
	maxTier      = 4
	sideTier     = 2 // the other path stops here once one path passes it
)

// path waypoints (tile centers); bots enter off the left edge, the factory sits at the end.
var waypoints = [][2]float64{{-1, 1}, {14, 1}, {14, 4}, {3, 4}, {3, 8}, {13, 8}, {13, 10}, {16, 10}}

// --- state ---

type Enemy struct {
	Kind      EnemyKind
	Dist      float64
	HP, MaxHP float64
	Slow      float64 // seconds left chilled
	SlowF     float64 // speed multiplier while chilled
	Stun      float64 // seconds left stopped
	Brittle   float64
}

type Tower struct {
	Kind  TowerKind
	X, Y  int
	Tier  [2]int // tiers bought on each path
	Spent int
	Pops  int
	cd    float64
	Aim   float64 // radians, for drawing
	Flash float64 // seconds left of muzzle flash
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

type Game struct {
	Lives   int
	Money   int
	Towers  []*Tower
	Enemies []*Enemy
	FX      []FX
	queue   []spawn
	Time    float64
	Round   int  // last round started
	Active  bool // a round is running
	Auto    bool // start the next round by itself
	Over    string
	Kills   int
	onEvent func(string)
}

func newGame() *Game { return &Game{Lives: startLives, Money: startMoney} }

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

var factoryTile = [2]int{int(waypoints[len(waypoints)-1][0]), int(waypoints[len(waypoints)-1][1])}

func buildable(x, y int) bool {
	t := [2]int{x, y}
	return x >= 0 && y >= 0 && x < boardW && y < boardH && !pathTiles[t] && t != factoryTile
}

// --- towers ---

func (t *Tower) stats() stats {
	sp := towerSpecs[t.Kind]
	s := sp.base
	for p := 0; p < 2; p++ {
		for i := 0; i < t.Tier[p]; i++ {
			sp.paths[p][i].mod(&s)
		}
	}
	return s
}

// nextUpgrade is path p's next upgrade, or ok=false when maxed or blocked by the other path.
func (t *Tower) nextUpgrade(p int) (u upgrade, ok bool, why string) {
	n := t.Tier[p]
	if n >= maxTier {
		return u, false, "maxed"
	}
	o := t.Tier[1-p]
	if n >= sideTier && o > sideTier {
		return u, false, "locked: other path"
	}
	return towerSpecs[t.Kind].paths[p][n], true, ""
}

func sellValue(t *Tower) int { return int(float64(t.Spent) * sellRefund) }

func (g *Game) towerAt(x, y int) *Tower {
	for _, t := range g.Towers {
		if t.X == x && t.Y == y {
			return t
		}
	}
	return nil
}

func (g *Game) build(k TowerKind, x, y int) bool {
	c := towerSpecs[k].cost
	if g.Over == "lose" || !buildable(x, y) || g.towerAt(x, y) != nil || g.Money < c {
		return false
	}
	g.Money -= c
	g.Towers = append(g.Towers, &Tower{Kind: k, X: x, Y: y, Spent: c, Aim: -math.Pi / 2})
	return true
}

func (g *Game) upgrade(t *Tower, p int) bool {
	u, ok, _ := t.nextUpgrade(p)
	if !ok || g.Money < u.cost || g.Over == "lose" {
		return false
	}
	g.Money -= u.cost
	t.Spent += u.cost
	t.Tier[p]++
	return true
}

func (g *Game) sell(t *Tower) {
	for i, o := range g.Towers {
		if o == t {
			g.Money += sellValue(t)
			g.Towers = append(g.Towers[:i], g.Towers[i+1:]...)
			return
		}
	}
}

// --- rounds ---

// wave returns round n's bots in spawn order.
func wave(n int) []EnemyKind {
	var w []EnemyKind
	add := func(k EnemyKind, c int) {
		for i := 0; i < c; i++ {
			w = append(w, k)
		}
	}
	add(Scrap, 8+3*n)
	if n >= 4 {
		add(Bolt, 2*(n-3))
	}
	if n >= 8 {
		add(Brute, (n-6)/2)
	}
	if n >= 13 {
		add(Hulk, (n-11)/2)
	}
	return w
}

func roundBonus(n int) int { return 100 + n*5 }

func (g *Game) startRound() bool {
	if g.Active || g.Over == "lose" {
		return false
	}
	g.Round++
	g.Active = true
	gap := spawnSpacing * math.Max(0.35, 1-0.02*float64(g.Round)) // later rounds pack tighter
	for i, k := range wave(g.Round) {
		g.queue = append(g.queue, spawn{k, g.Time + float64(i)*gap})
	}
	return true
}

// --- simulation ---

func (g *Game) update() {
	if g.Over == "lose" {
		return
	}
	g.Time += dt
	g.step()
	if g.Lives <= 0 {
		g.Lives = 0
		g.Over = "lose"
		return
	}
	if g.Active && len(g.queue) == 0 && len(g.Enemies) == 0 {
		g.Active = false
		b := roundBonus(g.Round)
		g.Money += b
		if g.Round == winRound && g.Over == "" {
			g.Over = "win"
			g.event("Round " + itoa(winRound) + " cleared: you win! Keep going in freeplay.")
		} else {
			g.event("Round " + itoa(g.Round) + " cleared  +$" + itoa(b))
		}
		if g.Auto {
			g.startRound()
		}
	}
}

func itoa(n int) string { return money(n)[1:] }

func (g *Game) step() {
	for len(g.queue) > 0 && g.queue[0].at <= g.Time {
		k := g.queue[0].kind
		hp := enemySpecs[k].hp * (1 + 0.05*float64(max(0, g.Round-10))) // late rounds toughen
		g.Enemies = append(g.Enemies, &Enemy{Kind: k, HP: hp, MaxHP: hp})
		g.queue = g.queue[1:]
	}
	for _, e := range g.Enemies {
		e.Slow = math.Max(0, e.Slow-dt)
		e.Stun = math.Max(0, e.Stun-dt)
		if e.Slow == 0 {
			e.SlowF, e.Brittle = 1, 0
		}
	}
	for _, t := range g.Towers {
		t.Flash = math.Max(0, t.Flash-dt)
		if t.Kind != Frost {
			continue
		}
		s := t.stats()
		for _, e := range g.Enemies {
			if inRange(t, e, s.rng) {
				chill(e, s.slow, s.brittle)
			}
		}
	}
	alive := g.Enemies[:0]
	for _, e := range g.Enemies {
		if e.Stun == 0 {
			sp := enemySpecs[e.Kind].speed
			if e.Slow > 0 {
				sp *= e.SlowF
			}
			e.Dist += sp * dt
		}
		if e.Dist >= pathLen {
			g.Lives -= enemySpecs[e.Kind].leak
			continue
		}
		alive = append(alive, e)
	}
	g.Enemies = alive

	for _, t := range g.Towers {
		g.fire(t)
	}

	alive = g.Enemies[:0]
	for _, e := range g.Enemies {
		if e.HP > 0 {
			alive = append(alive, e)
		}
	}
	g.Enemies = alive
	fx := g.FX[:0]
	for _, f := range g.FX {
		if f.t += dt; f.t < f.life {
			fx = append(fx, f)
		}
	}
	g.FX = fx
}

func chill(e *Enemy, slow, brittle float64) {
	if e.Slow == 0 || slow < e.SlowF {
		e.SlowF = slow
	}
	e.Brittle = math.Max(e.Brittle, brittle)
	e.Slow = 0.3
}

func (g *Game) fire(t *Tower) {
	s := t.stats()
	t.cd -= dt
	if t.cd > 0 {
		return
	}
	targets := g.firstN(t, s.rng, s.shots, s.pierce || t.Kind != Gun)
	if len(targets) == 0 {
		return
	}
	t.cd = 1 / s.rate
	t.Flash = 0.12
	cx, cy := float64(t.X), float64(t.Y)
	tx, ty := pos(targets[0].Dist)
	if t.Kind == Gun || t.Kind == Cannon {
		t.Aim = math.Atan2(ty-cy, tx-cx)
	}
	switch t.Kind {
	case Gun:
		for _, e := range targets {
			ex, ey := pos(e.Dist)
			if s.line { // railgun: every bot near the line through the target
				ux, uy := ex-cx, ey-cy
				l := math.Hypot(ux, uy)
				ux, uy = ux/l, uy/l
				ex, ey = cx+ux*s.rng*1.5, cy+uy*s.rng*1.5
				for _, o := range g.Enemies {
					ox, oy := pos(o.Dist)
					along := (ox-cx)*ux + (oy-cy)*uy
					if along > 0 && math.Abs((ox-cx)*uy-(oy-cy)*ux) < 0.4 {
						g.hit(t, o, s)
					}
				}
			} else {
				g.hit(t, e, s)
			}
			g.FX = append(g.FX, FX{kind: 0, x0: cx, y0: cy, x1: ex, y1: ey, life: 0.08})
		}
	case Cannon:
		g.FX = append(g.FX, FX{kind: 1, x0: tx, y0: ty, r: s.splash, life: 0.25})
		for _, e := range g.Enemies {
			ex, ey := pos(e.Dist)
			if math.Hypot(ex-tx, ey-ty) <= s.splash {
				g.hit(t, e, s)
			}
		}
	case Tesla:
		hitSet := map[*Enemy]bool{}
		px, py := cx, cy
		cur := targets[0]
		for j := 0; j < s.chain && cur != nil; j++ {
			ex, ey := pos(cur.Dist)
			g.FX = append(g.FX, FX{kind: 2, x0: px, y0: py, x1: ex, y1: ey, life: 0.15})
			hitSet[cur] = true
			g.hit(t, cur, s)
			if s.slow > 0 {
				chill(cur, s.slow, 0)
			}
			px, py = ex, ey
			cur = g.nearest(px, py, 1.6, hitSet)
		}
	case Frost:
		g.FX = append(g.FX, FX{kind: 3, x0: cx, y0: cy, r: s.rng, life: 0.4})
		for _, e := range g.Enemies {
			if inRange(t, e, s.rng) {
				g.hit(t, e, s)
			}
		}
	}
}

func (g *Game) hit(t *Tower, e *Enemy, s stats) {
	if e.HP <= 0 || s.dmg <= 0 {
		return
	}
	if enemySpecs[e.Kind].armored && !s.pierce {
		return
	}
	e.HP -= s.dmg + e.Brittle
	if s.stun > 0 {
		e.Stun = math.Max(e.Stun, s.stun)
	}
	if e.HP <= 0 {
		g.Money += enemySpecs[e.Kind].bounty
		g.Kills++
		t.Pops++
	}
}

func inRange(t *Tower, e *Enemy, r float64) bool {
	x, y := pos(e.Dist)
	return x >= -0.5 && math.Hypot(x-float64(t.X), y-float64(t.Y)) <= r
}

// firstN returns up to n bots in range, furthest along first.
func (g *Game) firstN(t *Tower, r float64, n int, armor bool) []*Enemy {
	var out []*Enemy
	for _, e := range g.Enemies {
		if e.HP > 0 && inRange(t, e, r) && (armor || !enemySpecs[e.Kind].armored) {
			out = append(out, e)
		}
	}
	for i := 1; i < len(out); i++ { // insertion sort: lists are short
		for j := i; j > 0 && out[j].Dist > out[j-1].Dist; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out[:min(n, len(out))]
}

func (g *Game) nearest(x, y, r float64, skip map[*Enemy]bool) *Enemy {
	var best *Enemy
	bd := r
	for _, e := range g.Enemies {
		ex, ey := pos(e.Dist)
		if d := math.Hypot(ex-x, ey-y); e.HP > 0 && !skip[e] && d <= bd {
			best, bd = e, d
		}
	}
	return best
}
