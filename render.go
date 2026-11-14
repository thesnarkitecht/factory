package main

import (
	"math"
)

type rgb struct{ r, g, b float64 } // 0..1

func hex(h uint32) rgb {
	return rgb{float64(h>>16&255) / 255, float64(h>>8&255) / 255, float64(h&255) / 255}
}

func (c rgb) mul(k float64) rgb { return rgb{c.r * k, c.g * k, c.b * k} }
func mix(a, b rgb, t float64) rgb {
	return rgb{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t}
}

// Sprite is premultiplied-free RGBA in 0..255.
type Sprite struct {
	w, h int
	px   []uint8
}

// Frame is the RGB pixel buffer of the map viewport.
type Frame struct {
	w, h int
	px   []uint8
}

func newFrame(w, h int) *Frame { return &Frame{w, h, make([]uint8, w*h*3)} }

// shader gets centered coords (units of the shorter tile side) and returns color+alpha.
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

// rotate maps screen coords into the building's frame: a forward, b sideways.
func rotate(dir int, x, y float64) (float64, float64) {
	fx, fy := float64(dx[dir]), float64(dy[dir])
	return x*fx + y*fy, -x*fy + y*fx
}

func frac(v float64) float64 { return v - math.Floor(v) }

func box(x, y, r float64) bool { return math.Abs(x) < r && math.Abs(y) < r }

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

var shadow = rgb{0, 0, 0}

// withShadow adds a soft drop shadow under a square body of half-size r.
func withShadow(r float64, f shader) shader {
	return func(x, y float64) (rgb, float64) {
		if c, a := f(x, y); a > 0 {
			return c, a
		}
		if box(x-0.04, y-0.05, r) {
			return shadow, 0.35
		}
		return rgb{}, 0
	}
}

var (
	steel   = hex(0x8a94a6)
	darkSt  = hex(0x3b4252)
	yellow  = hex(0xf2c14e)
	orange  = hex(0xff8c32)
	brickC  = hex(0xa4483a)
	mortar  = hex(0x6b5a4e)
	wood    = hex(0x8b5e3c)
	gold    = hex(0xe8b923)
	asmBlue = hex(0x4a6fa5)
	green   = hex(0x5fbf6a)
)

func beltShader(dir int, t float64) shader {
	return func(x, y float64) (rgb, float64) {
		a, b := rotate(dir, x, y)
		ab := math.Abs(b)
		switch {
		case ab < 0.36:
			c := hex(0x2b2d33)
			if frac(2*a+ab*1.4-t) < 0.22 {
				c = hex(0x50545e)
			}
			return c, 1
		case ab < 0.45:
			return mix(hex(0x9aa3b2), hex(0x5d6472), (ab-0.36)/0.09), 1
		}
		return rgb{}, 0
	}
}

func nub(a, b float64, c rgb) (rgb, bool) {
	if a > 0.34 && a < 0.5 && math.Abs(b) < 0.13 {
		return c, true
	}
	return rgb{}, false
}

func drillShader(dir int, rot float64, on bool) shader {
	return withShadow(0.42, func(x, y float64) (rgb, float64) {
		a, b := rotate(dir, x, y)
		if c, ok := nub(a, b, yellow); ok {
			return c, 1
		}
		if !box(x, y, 0.42) {
			return rgb{}, 0
		}
		r := math.Hypot(x, y)
		th := math.Atan2(y, x)
		switch {
		case r < 0.07:
			return darkSt, 1
		case r < 0.29:
			if frac((th+rot)*3/(2*math.Pi)) < 0.38 {
				c := hex(0xd9a441)
				if on {
					c = yellow
				}
				return c.mul(0.8 + 0.4*(1-r/0.29)), 1
			}
			return hex(0x22252b), 1
		case r < 0.33:
			return darkSt, 1
		}
		// hazard corners
		if math.Abs(x) > 0.3 && math.Abs(y) > 0.3 && frac((x+y)*6) < 0.5 {
			return yellow.mul(0.9), 1
		}
		return bevel(steel, x, y, 0.42), 1
	})
}

func furnaceShader(dir int, flick float64, on bool) shader {
	return withShadow(0.44, func(x, y float64) (rgb, float64) {
		if !box(x, y, 0.44) {
			return rgb{}, 0
		}
		a, b := rotate(dir, x, y)
		if a > 0.12 && math.Abs(b) < 0.2 {
			if !on {
				return hex(0x1d1a1a), 1
			}
			heat := 1 - math.Abs(b)/0.2*0.6 - (0.44-a)/0.32*0.4
			heat = heat*(0.85+0.15*flick) + 0.1*math.Sin(b*30+flick*6)
			return mix(hex(0xb8300c), hex(0xffe08a), clamp01(heat)), 1
		}
		row := math.Floor((y + 0.5) / 0.14)
		off := 0.0
		if int(row)%2 == 0 {
			off = 0.11
		}
		c := brickC
		if frac((y+0.5)/0.14) < 0.15 || frac((x+0.5+off)/0.22) < 0.1 {
			c = mortar
		} else if frac((x+0.5+off)/0.22) < 0.4 {
			c = c.mul(1.08)
		}
		if on && a > 0 && math.Abs(b) < 0.32 {
			c = mix(c, orange, 0.18) // glow spills onto the bricks
		}
		return bevel(c, x, y, 0.44), 1
	})
}

func gearShape(x, y, rot, r1, r2, hole float64, teeth int) bool {
	r := math.Hypot(x, y)
	th := math.Atan2(y, x) + rot
	if r < hole {
		return false
	}
	if r < r1 {
		return true
	}
	return r < r2 && frac(th*float64(teeth)/(2*math.Pi)) < 0.5
}

func asmShader(dir int, rot float64, on bool) shader {
	return withShadow(0.43, func(x, y float64) (rgb, float64) {
		a, b := rotate(dir, x, y)
		if c, ok := nub(a, b, green); ok {
			return c, 1
		}
		if !box(x, y, 0.43) {
			return rgb{}, 0
		}
		if gearShape(x, y, rot, 0.2, 0.28, 0.08, 8) {
			c := hex(0xc9ced8)
			if on {
				c = hex(0xe6ebf5)
			}
			return c, 1
		}
		if math.Hypot(x, y) < 0.32 {
			return hex(0x2a3b55), 1
		}
		// rivets
		if math.Abs(x) > 0.32 && math.Abs(y) > 0.32 && math.Hypot(math.Abs(x)-0.37, math.Abs(y)-0.37) < 0.035 {
			return steel, 1
		}
		return bevel(asmBlue, x, y, 0.43), 1
	})
}

func tradeShader() shader {
	return withShadow(0.44, func(x, y float64) (rgb, float64) {
		if !box(x, y, 0.44) {
			return rgb{}, 0
		}
		r := math.Hypot(x+0.02, y)
		switch {
		case r < 0.15 && (math.Abs(x+0.02) < 0.03 || (math.Abs(y) < 0.1 && frac((y+0.5)*10) < 0.5 && math.Abs(x+0.02) < 0.09)):
			return hex(0x9a6b06), 1
		case r < 0.22:
			return gold.mul(1 - 0.5*(x+y)), 1
		case r < 0.25:
			return hex(0x9a6b06), 1
		}
		c := wood
		if frac((y+0.5)/0.18) < 0.1 {
			c = wood.mul(0.6)
		}
		if math.Abs(math.Abs(x)-math.Abs(y)) < 0.03 {
			c = wood.mul(0.75) // crate cross bracing
		}
		return bevel(c, x, y, 0.44), 1
	})
}

var oreColor = [numItems]rgb{
	CopperOre: hex(0xd9824a), IronOre: hex(0x8fb3d9), Coal: hex(0x2a2a2e), Stone: hex(0xa8a29a),
}

func itemShader(it Item) shader {
	return func(x, y float64) (rgb, float64) {
		r := math.Hypot(x, y)
		th := math.Atan2(y, x)
		lit := 1.15 - 0.6*(x+y) // light from the top-left
		switch it {
		case CopperOre, IronOre, Coal, Stone:
			if r < 0.36+0.06*math.Sin(5*th+1)+0.03*math.Sin(11*th) {
				c := oreColor[it]
				if it == Coal { // lighter than ground coal so it reads on a dark belt
					c = hex(0x4a4a54)
				}
				if it == Coal && frac(x*7+y*3) < 0.2 {
					c = hex(0x7a7a88)
				}
				return c.mul(lit), 1
			}
		case CopperPlate, IronPlate:
			if box(x, y, 0.32) {
				c := hex(0xe39a62)
				if it == IronPlate {
					c = hex(0xc4ccd8)
				}
				return bevel(c, x, y, 0.32), 1
			}
		case Brick:
			if math.Abs(x) < 0.4 && math.Abs(y) < 0.22 {
				return brickC.mul(lit), 1
			}
		case Gear:
			if gearShape(x, y, 0, 0.24, 0.36, 0.1, 8) {
				return hex(0xb8c0cc).mul(lit), 1
			}
		case Wire:
			if r > 0.16 && r < 0.36 {
				c := hex(0xe08a4a)
				if frac(th*5/(2*math.Pi)) < 0.15 {
					c = c.mul(0.7)
				}
				return c.mul(lit), 1
			}
		case Circuit:
			if box(x, y, 0.36) {
				if math.Hypot(frac(x*5+0.5)-0.5, frac(y*5+0.5)-0.5) < 0.18 {
					return gold, 1
				}
				return bevel(hex(0x2f8f4e), x, y, 0.36), 1
			}
		case Engine:
			if math.Abs(x) < 0.4 && math.Abs(y) < 0.3 {
				switch {
				case math.Abs(y) < 0.05:
					return hex(0xd94a3a), 1
				case y < -0.12 && frac(x*4) < 0.6:
					return hex(0xd0d6e0), 1
				}
				return bevel(hex(0x3d4350), x, y, 0.4), 1
			}
		}
		return rgb{}, 0
	}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// hash gives a stable pseudo-random 0..1 for integer coords.
func hash(a, b, c int) float64 {
	h := uint32(a)*374761393 + uint32(b)*668265263 + uint32(c)*2246822519
	h = (h ^ (h >> 13)) * 1274126177
	h ^= h >> 16
	return float64(h&0xffff) / 65535
}

var grass = [4]rgb{hex(0x3f6b3a), hex(0x467541), hex(0x3a6236), hex(0x4d7d45)}

// groundColor shades a texel (8x8 per tile) of world tile tx,ty.
func groundColor(g *Game, tx, ty, u, v int) rgb {
	if !g.in(tx, ty) {
		return hex(0x15171c)
	}
	h := hash(tx*8+u, ty*8+v, 7)
	c := grass[int(h*4)%4]
	if ore := g.oreAt(tx, ty); ore != None {
		c = mix(hex(0x4a3f35), hex(0x55493d), h)
		// ore nuggets: clusters of texels
		if hash(tx*4+u/2, ty*4+v/2, int(ore)) < 0.42 {
			c = oreColor[ore].mul(0.75 + 0.5*hash(tx*8+u, ty*8+v, 3))
			if ore == Coal {
				c = hex(0x1c1c20).mul(1 + h)
			}
		}
	} else if h > 0.97 {
		c = hex(0x8fae5a) // little flowers / bright blades
	}
	if u == 0 || v == 0 {
		c = c.mul(0.92) // faint grid
	}
	return c
}

// spriteKey identifies a cached building/item sprite at the current zoom.
type spriteKey struct {
	kind  Kind
	item  Item
	dir   int
	frame int
	on    bool
}

const animFrames = 12

type Renderer struct {
	tw, th int // tile size in pixels
	cache  map[spriteKey]*Sprite
	bg     *Frame
	bgKey  [5]int
	frame  *Frame
	itemPx int
}

func newRenderer() *Renderer { return &Renderer{cache: map[spriteKey]*Sprite{}} }

func (r *Renderer) setTile(tw, th int) {
	if tw != r.tw || th != r.th {
		r.tw, r.th = tw, th
		r.cache = map[spriteKey]*Sprite{}
		r.bgKey = [5]int{}
	}
}

func (r *Renderer) sprite(k spriteKey) *Sprite {
	if s, ok := r.cache[k]; ok {
		return s
	}
	t := float64(k.frame) / animFrames
	var f shader
	w, h := r.tw, r.th
	switch k.kind {
	case Belt:
		f = beltShader(k.dir, t)
	case Drill:
		f = drillShader(k.dir, t*2*math.Pi/3, k.on)
	case Furnace:
		f = furnaceShader(k.dir, math.Sin(t*2*math.Pi)*0.5+0.5, k.on)
	case Assembler:
		f = asmShader(k.dir, t*2*math.Pi/8, k.on)
	case Trade:
		f = tradeShader()
	default:
		s := max(3, min(w, h)*9/20)
		w, h = s, s
		f = itemShader(k.item)
	}
	s := makeSprite(w, h, f)
	r.cache[k] = s
	return s
}

// draw renders the visible map into r.frame and returns it.
func (r *Renderer) draw(g *Game, camX, camY, fw, fh, curX, curY int, sel Kind, selDir int, t float64) *Frame {
	key := [5]int{camX, camY, fw, fh, r.tw*1000 + r.th}
	if r.bg == nil || r.bgKey != key {
		r.bg = newFrame(fw, fh)
		for py := 0; py < fh; py++ {
			for px := 0; px < fw; px++ {
				tx, ty := camX+px/r.tw, camY+py/r.th
				c := groundColor(g, tx, ty, (px%r.tw)*8/r.tw, (py%r.th)*8/r.th)
				i := (py*fw + px) * 3
				r.bg.px[i], r.bg.px[i+1], r.bg.px[i+2] = to8(c.r), to8(c.g), to8(c.b)
			}
		}
		r.bgKey = key
		r.frame = newFrame(fw, fh)
	}
	f := r.frame
	copy(f.px, r.bg.px)
	fr := int(t*animFrames*2) % animFrames
	type itemDraw struct {
		it   Item
		x, y int
	}
	var items []itemDraw
	for ty := camY; ty <= camY+fh/r.th; ty++ {
		for tx := camX; tx <= camX+fw/r.tw; tx++ {
			b := g.at(tx, ty)
			if b == nil {
				continue
			}
			px, py := (tx-camX)*r.tw, (ty-camY)*r.th
			on := b.working()
			k := spriteKey{kind: b.Kind, dir: b.Dir, on: on}
			if b.Kind == Trade {
				k.dir = 0
			}
			if b.Kind == Belt || on {
				k.frame = fr
			}
			f.blit(r.sprite(k), px, py, 1)
			if b.Kind == Belt && b.Item != None {
				p := b.Prog - 0.5
				items = append(items, itemDraw{b.Item, px + r.tw/2 + int(p*float64(dx[b.Dir]*r.tw)), py + r.th/2 + int(p*float64(dy[b.Dir]*r.th))})
			}
			if b.Status != "" && b.Kind != Belt && int(t*2)%2 == 0 {
				f.dot(px+r.tw*5/6, py+r.th/6, max(1, min(r.tw, r.th)/10), hex(0xff4d4d))
			}
		}
	}
	for _, it := range items { // items above belts
		s := r.sprite(spriteKey{item: it.it})
		f.blit(s, it.x-s.w/2, it.y-s.h/2, 1)
	}
	// ghost preview + cursor
	cx, cy := (curX-camX)*r.tw, (curY-camY)*r.th
	ok := sel != Empty && g.unlocked(sel) && g.Money >= kindCost[sel]
	if sel != Empty && g.at(curX, curY) == nil {
		d := selDir
		if sel == Trade {
			d = 0
		}
		f.blit(r.sprite(spriteKey{kind: sel, dir: d}), cx, cy, 0.45)
	}
	pulse := 0.6 + 0.4*math.Sin(t*6)
	col := mix(hex(0x7fd1ff), hex(0xffffff), pulse)
	if !ok && sel != Empty {
		col = hex(0xff6b6b)
	}
	f.rect(cx, cy, r.tw, r.th, max(1, min(r.tw, r.th)/16), col)
	return f
}

func (f *Frame) set(x, y int, c rgb) {
	if x >= 0 && y >= 0 && x < f.w && y < f.h {
		i := (y*f.w + x) * 3
		f.px[i], f.px[i+1], f.px[i+2] = to8(c.r), to8(c.g), to8(c.b)
	}
}

func (f *Frame) rect(x, y, w, h, t int, c rgb) {
	for i := 0; i < t; i++ {
		for px := x; px < x+w; px++ {
			f.set(px, y+i, c)
			f.set(px, y+h-1-i, c)
		}
		for py := y; py < y+h; py++ {
			f.set(x+i, py, c)
			f.set(x+w-1-i, py, c)
		}
	}
}

func (f *Frame) dot(cx, cy, r int, c rgb) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				f.set(cx+x, cy+y, c)
			}
		}
	}
}
