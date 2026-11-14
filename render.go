package main

import (
	"math"
	"sort"
)

type rgb struct{ r, g, b float64 } // 0..1

func hex(h uint32) rgb {
	return rgb{float64(h>>16&255) / 255, float64(h>>8&255) / 255, float64(h&255) / 255}
}

func (c rgb) mul(k float64) rgb { return rgb{c.r * k, c.g * k, c.b * k} }
func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// Sprite is straight RGBA in 0..255.
type Sprite struct {
	w, h int
	px   []uint8
}

// Frame is the RGB pixel buffer sent to the terminal.
type Frame struct {
	w, h int
	px   []uint8
}

func newFrame(w, h int) *Frame { return &Frame{w, h, make([]uint8, w*h*3)} }

// shader gets centered coords (units of the shorter side) and returns color+alpha.
type shader func(x, y float64) (rgb, float64)

const ss = 3 // supersampling per axis: cheap AA, sprites are cached

func makeSprite(w, h int, f shader) *Sprite {
	s := &Sprite{w, h, make([]uint8, w*h*4)}
	unit := float64(min(w, h))
	for py := 0; py < h; py++ {
		for px := 0; px < w; px++ {
			var acc rgb
			var aa float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss - float64(w)/2) / unit
					y := (float64(py) + (float64(sy)+0.5)/ss - float64(h)/2) / unit
					c, a := f(x, y)
					acc = rgb{acc.r + c.r*a, acc.g + c.g*a, acc.b + c.b*a}
					aa += a
				}
			}
			i := (py*w + px) * 4
			if aa > 0 {
				s.px[i], s.px[i+1], s.px[i+2] = to8(acc.r/aa), to8(acc.g/aa), to8(acc.b/aa)
				s.px[i+3] = to8(aa / ss / ss)
			}
		}
	}
	return s
}

func to8(v float64) uint8 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return uint8(v*255 + 0.5)
}

// blit draws s with its top-left at x,y, scaled by alpha k.
func (f *Frame) blit(s *Sprite, x, y int, k float64) {
	for sy := 0; sy < s.h; sy++ {
		fy := y + sy
		if fy < 0 || fy >= f.h {
			continue
		}
		for sx := 0; sx < s.w; sx++ {
			fx := x + sx
			if fx < 0 || fx >= f.w {
				continue
			}
			si := (sy*s.w + sx) * 4
			a := float64(s.px[si+3]) / 255 * k
			if a <= 0 {
				continue
			}
			fi := (fy*f.w + fx) * 3
			for c := 0; c < 3; c++ {
				f.px[fi+c] = uint8(float64(f.px[fi+c])*(1-a) + float64(s.px[si+c])*a)
			}
		}
	}
}

func (f *Frame) blend(x, y int, c rgb, a float64) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h || a <= 0 {
		return
	}
	i := (y*f.w + x) * 3
	for k, v := range [3]float64{c.r, c.g, c.b} {
		f.px[i+k] = uint8(float64(f.px[i+k])*(1-a) + 255*clamp01(v)*a)
	}
}

func (f *Frame) fill(x, y, w, h int, c rgb, a float64) {
	for py := y; py < y+h; py++ {
		for px := x; px < x+w; px++ {
			f.blend(px, py, c, a)
		}
	}
}

func (f *Frame) disc(cx, cy, r float64, c rgb, a float64) {
	for y := int(cy - r - 1); y <= int(cy+r+1); y++ {
		for x := int(cx - r - 1); x <= int(cx+r+1); x++ {
			d := math.Hypot(float64(x)+0.5-cx, float64(y)+0.5-cy)
			f.blend(x, y, c, a*clamp01(r-d+0.5))
		}
	}
}

func (f *Frame) ring(cx, cy, r, w float64, c rgb, a float64, dashed bool) {
	for y := int(cy - r - w - 1); y <= int(cy+r+w+1); y++ {
		for x := int(cx - r - w - 1); x <= int(cx+r+w+1); x++ {
			px, py := float64(x)+0.5-cx, float64(y)+0.5-cy
			d := math.Abs(math.Hypot(px, py) - r)
			if dashed && frac(math.Atan2(py, px)*12/math.Pi) < 0.45 {
				continue
			}
			f.blend(x, y, c, a*clamp01(w/2-d+0.5))
		}
	}
}

func (f *Frame) line(x0, y0, x1, y1, w float64, c rgb, a float64) {
	n := int(math.Max(math.Abs(x1-x0), math.Abs(y1-y0))) + 1
	for i := 0; i <= n; i++ {
		t := float64(i) / float64(n)
		f.disc(x0+(x1-x0)*t, y0+(y1-y0)*t, w/2, c, a)
	}
}

func (f *Frame) rect(x, y, w, h, t int, c rgb) {
	f.fill(x, y, w, t, c, 1)
	f.fill(x, y+h-t, w, t, c, 1)
	f.fill(x, y, t, h, c, 1)
	f.fill(x+w-t, y, t, h, c, 1)
}

// --- shapes ---

// Dirs: 0 up, 1 right, 2 down, 3 left.
var dx = [4]int{0, 1, 0, -1}
var dy = [4]int{-1, 0, 1, 0}

// rotate maps screen coords into a frame where a is forward along dir and b is sideways.
func rotate(dir int, x, y float64) (float64, float64) {
	fx, fy := float64(dx[dir]), float64(dy[dir])
	return x*fx + y*fy, -x*fy + y*fx
}

func frac(v float64) float64        { return v - math.Floor(v) }
func box(x, y, r float64) bool      { return math.Abs(x) < r && math.Abs(y) < r }
func clamp01(v float64) float64     { return math.Max(0, math.Min(1, v)) }
func lerpC(a, b rgb, t float64) rgb { return mix(a, b, clamp01(t)) }

// bevel shades a square body: light top-left edge, dark bottom-right edge.
func bevel(c rgb, x, y, r float64) rgb {
	e := 0.06
	switch {
	case x < -r+e || y < -r+e:
		return c.mul(1.25)
	case x > r-e || y > r-e:
		return c.mul(0.6)
	}
	return c
}

// beltShader draws a conveyor tile; turn 1/2 curves in from the left/right side.
func beltShader(dir, turn int, t float64) shader {
	return func(x, y float64) (rgb, float64) {
		a, b := rotate(dir, x, y)
		along, off := a, b
		if turn != 0 {
			if turn == 2 {
				b = -b
			}
			ra, rb := a-0.5, b+0.5
			off = math.Hypot(ra, rb) - 0.5
			along = (math.Pi-math.Atan2(rb, ra))/(math.Pi/2)*0.785 - 0.5
		}
		ab := math.Abs(off)
		switch {
		case ab < 0.34:
			c := hex(0x2b2d33)
			if frac(2*along+ab*1.4-t) < 0.22 {
				c = hex(0x4a4e58)
			}
			return c, 1
		case ab < 0.44:
			return mix(hex(0xb7bfcc), hex(0x5d6472), (ab-0.34)/0.1), 1
		}
		return rgb{}, 0
	}
}

// factoryShader is the brick works the bots are trying to reach.
func factoryShader(flick float64) shader {
	return func(x, y float64) (rgb, float64) {
		if x > 0.12 && x < 0.3 && y > -0.5 && y < -0.2 { // chimney
			if y < -0.44 {
				return hex(0x2a2a2a), 1
			}
			return bevel(hex(0x7a3a30), x-0.21, y+0.35, 0.09), 1
		}
		if !box(x, y+0.06, 0.42) || y < -0.3 {
			if box(x-0.04, y+0.01, 0.42) && y >= -0.25 {
				return rgb{}, 0.35 // shadow
			}
			return rgb{}, 0
		}
		if x < -0.12 && math.Abs(y-0.12) < 0.18 { // glowing door facing the conveyor
			heat := 1 - (x+0.42)/0.3*0.5 + 0.15*math.Sin(y*25+flick*6)
			return lerpC(hex(0xb8300c), hex(0xffe08a), heat*(0.85+0.15*flick)), 1
		}
		row := math.Floor((y + 0.5) / 0.12)
		off := 0.0
		if int(row)%2 == 0 {
			off = 0.1
		}
		c := hex(0xa4483a)
		if frac((y+0.5)/0.12) < 0.15 || frac((x+0.5+off)/0.2) < 0.1 {
			c = hex(0x6b5a4e)
		}
		if math.Abs(x-0.2) < 0.08 && math.Abs(y-0.0) < 0.07 {
			c = hex(0xffd479) // lit window
		}
		return bevel(c, x, y+0.06, 0.42), 1
	}
}

var palette = map[byte]uint32{
	'k': 0x1a1c24, 'g': 0x4b5263, 'G': 0x8a94a6, 'w': 0xeef2f8, 'y': 0xf2c14e,
	'o': 0xd9824a, 'O': 0x8a4a22, 'r': 0xd9534f, 'R': 0x8e2a2a, 'n': 0xa87b4f,
	'N': 0x6b4a2e, 'b': 0x5ec8ff, 'B': 0x2b6cb0, 'c': 0x6fd6ff, 'C': 0xbff0ff,
}

// spriteShader samples a 16x16 pixel-art sprite, rotated by rot around the pivot.
func spriteShader(name string, acc, accDark uint32, rot, pivX, pivY float64, tint rgb, tintK float64, shadow bool) shader {
	rows := sprites[name]
	cs, sn := math.Cos(rot), math.Sin(rot)
	sample := func(x, y float64) (rgb, bool) {
		xr, yr := x*cs+y*sn, -x*sn+y*cs
		u, v := int(math.Floor(pivX+xr*16)), int(math.Floor(pivY+yr*16))
		if u < 0 || v < 0 || u > 15 || v > 15 {
			return rgb{}, false
		}
		ch := rows[v][u]
		switch ch {
		case '.':
			return rgb{}, false
		case 'A':
			return hex(acc), true
		case 'a':
			return hex(accDark), true
		}
		return hex(palette[ch]), true
	}
	return func(x, y float64) (rgb, float64) {
		if c, ok := sample(x, y); ok {
			return mix(c, tint, tintK), 1
		}
		if _, ok := sample(x-0.06, y-0.07); ok && shadow {
			return rgb{}, 0.3
		}
		return rgb{}, 0
	}
}

var towerAcc = [numTower][2]uint32{{0x5b8def, 0x2f4f8f}, {0xb07a3a, 0x6b4520}, {0xd9824a, 0x8a4a22}, {0x6fd6ff, 0x2b6cb0}}

func baseShader(k TowerKind) shader {
	led := hex(towerAcc[k][0])
	return func(x, y float64) (rgb, float64) {
		ax, ay := math.Abs(x), math.Abs(y)
		if ax+ay > 0.62 || !box(x, y, 0.42) { // octagonal plate
			if box(x-0.04, y-0.05, 0.42) && math.Abs(x-0.04)+math.Abs(y-0.05) <= 0.62 {
				return rgb{}, 0.35
			}
			return rgb{}, 0
		}
		if math.Hypot(ax-0.3, ay-0.16) < 0.04 || math.Hypot(ax-0.16, ay-0.3) < 0.04 {
			return led, 1
		}
		c := hex(0x5d6472)
		if frac((x+y)*5) < 0.08 {
			c = c.mul(0.9)
		}
		return bevel(c, x, y, 0.42), 1
	}
}

// --- hashing / ground ---

func hash(a, b, c int) float64 {
	h := uint32(a)*374761393 + uint32(b)*668265263 + uint32(c)*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h&0xffff) / 65535
}

var grass = [4]rgb{hex(0x3f6b3a), hex(0x467541), hex(0x3a6236), hex(0x4d7d45)}

func groundColor(tx, ty, u, v int) rgb {
	h := hash(tx*8+u, ty*8+v, 7)
	c := grass[int(h*4)%4]
	switch {
	case h > 0.998:
		c = hex(0xd8c86a) // flowers
	case h > 0.985:
		c = hex(0x8fae5a)
	}
	if u == 0 || v == 0 {
		c = c.mul(0.94) // faint build grid
	}
	return c
}

// --- renderer ---

type sprKey struct {
	name    string
	size    int
	rot     int
	variant int
}

const animFrames = 12

type Renderer struct {
	cache map[sprKey]*Sprite
	bg    map[int]*Frame // board ground by tile size
}

func newRenderer() *Renderer { return &Renderer{cache: map[sprKey]*Sprite{}, bg: map[int]*Frame{}} }

func (r *Renderer) get(k sprKey, w, h int, f func() shader) *Sprite {
	if s, ok := r.cache[k]; ok {
		return s
	}
	if len(r.cache) > 4000 {
		r.cache = map[sprKey]*Sprite{} // resize churn: start over
	}
	s := makeSprite(w, h, f())
	r.cache[k] = s
	return s
}

func (r *Renderer) ground(T int) *Frame {
	if f, ok := r.bg[T]; ok {
		return f
	}
	f := newFrame(boardW*T, boardH*T)
	for py := 0; py < f.h; py++ {
		for px := 0; px < f.w; px++ {
			c := groundColor(px/T, py/T, (px%T)*8/T, (py%T)*8/T)
			i := (py*f.w + px) * 3
			f.px[i], f.px[i+1], f.px[i+2] = to8(c.r), to8(c.g), to8(c.b)
		}
	}
	r.bg[T] = f
	return f
}

func dirOf(a, b [2]int) int {
	for d := 0; d < 4; d++ {
		if a[0]+dx[d] == b[0] && a[1]+dy[d] == b[1] {
			return d
		}
	}
	return 1
}

type beltTile struct {
	t         [2]int
	dir, turn int
}

var belts = func() []beltTile {
	p := pathOrder()
	var out []beltTile
	for i := 0; i+1 < len(p); i++ {
		prev := [2]int{p[i][0] - 1, p[i][1]}
		if i > 0 {
			prev = p[i-1]
		}
		in, o := dirOf(prev, p[i]), dirOf(p[i], p[i+1])
		turn := 0
		switch o {
		case (in + 3) % 4:
			turn = 1
		case (in + 1) % 4:
			turn = 2
		}
		out = append(out, beltTile{p[i], o, turn})
	}
	return out
}()

// View says where things landed in the last frame, for mouse picking.
type View struct {
	T              int // your board's tile size in pixels
	RivalX, RivalY int // rival board origin
	RivalT         int // rival tile size (0 = hidden)
}

// draw renders both boards into a fw x fh frame.
func (r *Renderer) draw(g *Game, fw, fh int, cur [2]int, sel int, t float64, rival bool) (*Frame, View) {
	f := newFrame(fw, fh)
	f.fill(0, 0, fw, fh, hex(0x10131a), 1)
	v := View{T: max(2, min(fw/boardW, fh/boardH))}
	if rival {
		// rival at half size: beside your board or under it, whichever leaves bigger tiles
		side := min(fw*2/(boardW*3+2), fh/boardH)
		stack := min(fw/boardW, fh*2/(boardH*3+2))
		if v.T = max(side, stack); side >= stack {
			v.RivalX = boardW*v.T + v.T/2
		} else {
			v.RivalY = boardH*v.T + v.T/2
		}
		if v.RivalT = v.T / 2; v.RivalT < 4 {
			v = View{T: max(2, min(fw/boardW, fh/boardH))}
		}
	}
	r.drawBoard(f, g, g.You, 0, 0, v.T, t, cur, sel)
	if v.RivalT > 0 {
		r.drawBoard(f, g, g.Rival, v.RivalX, v.RivalY, v.RivalT, t, [2]int{-9, -9}, -1)
	}
	return f, v
}

func (r *Renderer) drawBoard(f *Frame, g *Game, b *Board, ox, oy, T int, t float64, cur [2]int, sel int) {
	bg := r.ground(T)
	for y := 0; y < bg.h && oy+y < f.h; y++ {
		w := min(bg.w, f.w-ox)
		if w > 0 {
			copy(f.px[((oy+y)*f.w+ox)*3:((oy+y)*f.w+ox+w)*3], bg.px[y*bg.w*3:(y*bg.w+w)*3])
		}
	}
	fr := int(t*animFrames*1.5) % animFrames
	for _, bt := range belts {
		s := r.get(sprKey{"belt", T, bt.dir*3 + bt.turn, fr}, T, T, func() shader {
			return beltShader(bt.dir, bt.turn, float64(fr)/animFrames)
		})
		f.blit(s, ox+bt.t[0]*T, oy+bt.t[1]*T, 1)
	}
	// entry gate: hazard stripes on the left edge
	ey := oy + int(waypoints[0][1])*T
	for y := 0; y < T; y++ {
		c := hex(0xf2c14e)
		if (y*6/T)%2 == 1 {
			c = hex(0x1a1c24)
		}
		f.fill(ox, ey+y, max(1, T/8), 1, c, 1)
	}
	ffr := int(t*8) % 8
	fs := r.get(sprKey{"factory", T, 0, ffr}, T, T, func() shader {
		return factoryShader(math.Sin(float64(ffr)/8*2*math.Pi)*0.5 + 0.5)
	})
	fx0, fy0 := ox+factoryTile[0]*T, oy+factoryTile[1]*T
	f.blit(fs, fx0, fy0, 1)
	for i := 0; i < 3; i++ { // chimney smoke
		p := frac(t*0.5 + float64(i)/3)
		f.disc(float64(fx0)+0.71*float64(T)+p*0.3*float64(T), float64(fy0)-p*0.8*float64(T), float64(T)*(0.06+0.08*p), hex(0xc8ccd4), 0.55*(1-p))
	}

	px := func(x, y float64) (float64, float64) {
		return float64(ox) + (x+0.5)*float64(T), float64(oy) + (y+0.5)*float64(T)
	}

	// range preview under everything else
	if cur[0] >= 0 {
		if tw := b.towerAt(cur[0], cur[1]); tw != nil {
			cx, cy := px(float64(tw.X), float64(tw.Y))
			f.ring(cx, cy, tw.stats().rng*float64(T), math.Max(1, float64(T)/14), hex(0xffffff), 0.55, true)
		} else if sel >= 0 {
			cx, cy := px(float64(cur[0]), float64(cur[1]))
			f.ring(cx, cy, towerSpecs[sel].rng*float64(T), math.Max(1, float64(T)/14), hex(0xffffff), 0.45, true)
		}
	}

	for _, tw := range b.Towers {
		r.drawTower(f, tw.Kind, tw.Lvl, tw.Aim, ox+tw.X*T, oy+tw.Y*T, T, 1, tw.Flash > 0)
	}

	es := append([]*Enemy(nil), b.Enemies...)
	sort.Slice(es, func(i, j int) bool { return es[i].Dist < es[j].Dist })
	for _, e := range es {
		sp := enemySpecs[e.Kind]
		x, y := pos(e.Dist)
		cx, cy := px(x, y)
		P := max(3, int(float64(T)*sp.size*1.6))
		rot := 0
		if e.Kind == Bolt {
			x2, y2 := pos(e.Dist + 0.05)
			rot = int(math.Round(math.Atan2(y2-y, x2-x)/(math.Pi/2))+4) % 4
		}
		slowed := 0
		if e.Slow > 0 {
			slowed = 1
		}
		name := [numEnemy]string{"scrap", "bolt", "brute", "hulk"}[e.Kind]
		s := r.get(sprKey{name, P, rot, slowed}, P, P, func() shader {
			return spriteShader(name, 0, 0, float64(rot)*math.Pi/2, 8, 8, hex(0x9fe6ff), 0.45*float64(slowed), true)
		})
		bob := 0
		if e.Kind != Bolt && int(e.Dist*6)%2 == 0 {
			bob = max(1, T/24)
		}
		f.blit(s, int(cx)-P/2, int(cy)-P/2-bob, 1)
		if e.HP < e.MaxHP {
			w := max(4, P*3/4)
			bx, by := int(cx)-w/2, int(cy)-P/2-max(2, T/8)
			hh := max(1, T/16)
			f.fill(bx, by, w, hh, hex(0x3a1010), 0.9)
			f.fill(bx, by, int(float64(w)*clamp01(e.HP/e.MaxHP)), hh, hex(0x6ee07a), 1)
		}
	}

	for _, e := range b.FX {
		a := 1 - e.t/e.life
		x0, y0 := px(e.x0, e.y0)
		x1, y1 := px(e.x1, e.y1)
		lw := math.Max(1, float64(T)/12)
		switch e.kind {
		case 0:
			f.line(x0, y0, x1, y1, lw, hex(0xfff1a8), a)
		case 1:
			rr := e.r * float64(T) * (0.5 + 0.5*e.t/e.life)
			f.disc(x0, y0, rr, hex(0xff8c32), 0.55*a)
			f.disc(x0, y0, rr*0.5, hex(0xffe08a), 0.8*a)
		case 2:
			n := 5
			lx, ly := x0, y0
			for i := 1; i <= n; i++ {
				tt := float64(i) / float64(n)
				jx := (hash(int(e.x1*97), i, int(t*20)) - 0.5) * float64(T) * 0.35
				jy := (hash(int(e.y1*89), i, int(t*20)+7) - 0.5) * float64(T) * 0.35
				if i == n {
					jx, jy = 0, 0
				}
				nx, ny := x0+(x1-x0)*tt+jx, y0+(y1-y0)*tt+jy
				f.line(lx, ly, nx, ny, lw*2, hex(0x6fd6ff), 0.5*a)
				f.line(lx, ly, nx, ny, lw, hex(0xffffff), a)
				lx, ly = nx, ny
			}
		case 3:
			f.ring(x0, y0, e.r*float64(T)*(e.t/e.life), lw*1.5, hex(0xbff0ff), 0.6*a, false)
		}
	}

	if cur[0] >= 0 {
		cx, cy := ox+cur[0]*T, oy+cur[1]*T
		ok := true
		if sel >= 0 && b.towerAt(cur[0], cur[1]) == nil {
			ok = buildable(cur[0], cur[1]) && b.Money >= towerSpecs[sel].cost
			if buildable(cur[0], cur[1]) {
				r.drawTower(f, TowerKind(sel), 1, -math.Pi/2, cx, cy, T, 0.5, false)
			}
		}
		col := mix(hex(0x7fd1ff), hex(0xffffff), 0.5+0.5*math.Sin(t*6))
		if !ok {
			col = hex(0xff6b6b)
		}
		f.rect(cx, cy, T, T, max(1, T/16), col)
	}
}

var headPivot = [numTower][2]float64{{6, 8}, {6, 8}, {8, 8}, {8, 8}}
var headName = [numTower]string{"gun", "cannon", "tesla", "frost"}

func (r *Renderer) drawTower(f *Frame, k TowerKind, lvl int, aim float64, x, y, T int, alpha float64, flash bool) {
	base := r.get(sprKey{"base", T, int(k), 0}, T, T, func() shader { return baseShader(k) })
	f.blit(base, x, y, alpha)
	rot := 0
	if k == Gun || k == Cannon {
		rot = int(math.Round(aim/(2*math.Pi)*32)+64) % 32
	}
	hs := r.get(sprKey{headName[k], T, rot, 0}, T, T, func() shader {
		p := headPivot[k]
		return spriteShader(headName[k], towerAcc[k][0], towerAcc[k][1], float64(rot)*2*math.Pi/32, p[0], p[1], rgb{}, 0, true)
	})
	f.blit(hs, x, y, alpha)
	cx, cy := float64(x)+float64(T)/2, float64(y)+float64(T)/2
	if flash {
		switch k {
		case Gun, Cannon:
			a := float64(rot) * 2 * math.Pi / 32
			f.disc(cx+math.Cos(a)*0.5*float64(T), cy+math.Sin(a)*0.5*float64(T), float64(T)*0.1, hex(0xffe08a), 0.9)
		case Tesla:
			f.disc(cx, cy-0.22*float64(T), float64(T)*0.18, hex(0xbff0ff), 0.6)
		}
	}
	for i := 0; i < lvl-1; i++ { // upgrade pips
		f.disc(float64(x)+float64(T)*(0.2+0.14*float64(i)), float64(y)+float64(T)*0.88, math.Max(1, float64(T)/18), hex(0xffd479), alpha)
	}
}
