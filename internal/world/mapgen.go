package world

import "math/rand/v2"

// Cell is a static map tile.
type Cell uint8

const (
	Floor Cell = iota
	Wall
)

// Grid is the static map. It never changes after generation.
type Grid struct {
	W, H  int
	Cells []Cell // row-major, len W*H
}

// At returns the cell at (x, y); out of bounds reads as Wall.
func (g *Grid) At(x, y int) Cell {
	if x < 0 || y < 0 || x >= g.W || y >= g.H {
		return Wall
	}
	return g.Cells[y*g.W+x]
}

func (g *Grid) set(x, y int, c Cell) { g.Cells[y*g.W+x] = c }

const (
	minLeafW, minLeafH = 12, 8 // BSP leaves are never split below 2x these
	minRoomW, minRoomH = 4, 3
	// MinMapW and MinMapH are the smallest sizes Generate accepts.
	MinMapW, MinMapH = minLeafW + 2, minLeafH + 2
)

// Generate builds a map by binary space partitioning: split the area into
// leaves, carve one room per leaf, and join sibling subtrees with an L-shaped
// corridor. Because every split joins its two halves, the map is connected.
// The border is always wall.
func Generate(rng *rand.Rand, w, h int) *Grid {
	if w < MinMapW || h < MinMapH {
		panic("world: map too small")
	}
	g := &Grid{W: w, H: h, Cells: make([]Cell, w*h)}
	for i := range g.Cells {
		g.Cells[i] = Wall
	}
	gen := mapGen{g: g, rng: rng}
	gen.split(1, 1, w-2, h-2)
	return g
}

type mapGen struct {
	g   *Grid
	rng *rand.Rand
}

// split partitions the rect and returns a floor point inside it to connect to.
func (m *mapGen) split(x, y, w, h int) (int, int) {
	canV, canH := w >= 2*minLeafW, h >= 2*minLeafH
	if !canV && !canH {
		return m.room(x, y, w, h)
	}
	// Terminal cells are about twice as tall as wide, so compare w against 2h.
	vertical := canV && (!canH || w > 2*h || (w*2 >= h && m.rng.IntN(2) == 0))
	var ax, ay, bx, by int
	if vertical {
		cut := minLeafW + m.rng.IntN(w-2*minLeafW+1)
		ax, ay = m.split(x, y, cut, h)
		bx, by = m.split(x+cut, y, w-cut, h)
	} else {
		cut := minLeafH + m.rng.IntN(h-2*minLeafH+1)
		ax, ay = m.split(x, y, w, cut)
		bx, by = m.split(x, y+cut, w, h-cut)
	}
	m.corridor(ax, ay, bx, by)
	return ax, ay
}

// room carves a room inside the leaf with a 1-cell margin and returns its centre.
func (m *mapGen) room(x, y, w, h int) (int, int) {
	rw := minRoomW + m.rng.IntN(w-2-minRoomW+1)
	rh := minRoomH + m.rng.IntN(h-2-minRoomH+1)
	rx := x + 1 + m.rng.IntN(w-2-rw+1)
	ry := y + 1 + m.rng.IntN(h-2-rh+1)
	for yy := ry; yy < ry+rh; yy++ {
		for xx := rx; xx < rx+rw; xx++ {
			m.g.set(xx, yy, Floor)
		}
	}
	return rx + rw/2, ry + rh/2
}

func (m *mapGen) corridor(x1, y1, x2, y2 int) {
	if m.rng.IntN(2) == 0 {
		m.hline(x1, x2, y1)
		m.vline(y1, y2, x2)
	} else {
		m.vline(y1, y2, x1)
		m.hline(x1, x2, y2)
	}
}

func (m *mapGen) hline(x1, x2, y int) {
	for x := min(x1, x2); x <= max(x1, x2); x++ {
		m.g.set(x, y, Floor)
	}
}

func (m *mapGen) vline(y1, y2, x int) {
	for y := min(y1, y2); y <= max(y1, y2); y++ {
		m.g.set(x, y, Floor)
	}
}
