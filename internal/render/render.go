// Package render turns a world.View into terminal bytes. A Frame is a grid of
// styled cells; Diff emits only the cells that changed since the previous
// frame, which is what keeps per-tick SSH traffic small.
package render

import (
	"fmt"
	"strconv"

	"github.com/AaronRaj06/lastprocess/internal/ecs"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

// Cell is one terminal cell.
type Cell struct {
	Ch    byte
	Style uint8
}

// Styles index into sgr. Every sequence starts with a reset so styles never leak into each other.
const (
	sText uint8 = iota
	sWall
	sFloor
	sOutside
	sMe
	sEnemy
	sProjectile
	sHealth
	sAmmo
	sShield
	sHUD
)

var sgr = [...]string{
	sText: "\x1b[0m", sWall: "\x1b[0;37m", sFloor: "\x1b[0;90m", sOutside: "\x1b[0;2;31m",
	sMe: "\x1b[0;1;92m", sEnemy: "\x1b[0;1;91m", sProjectile: "\x1b[0;1;93m",
	sHealth: "\x1b[0;92m", sAmmo: "\x1b[0;96m", sShield: "\x1b[0;94m", sHUD: "\x1b[0;1m",
}

var pickupCells = [...]Cell{world.Health: {'+', sHealth}, world.Ammo: {'=', sAmmo}, world.Shield: {'o', sShield}}

// Frame is a W x H grid of cells, row-major.
type Frame struct {
	W, H  int
	Cells []Cell
}

func (f *Frame) resize(w, h int) {
	f.W, f.H = w, h
	if cap(f.Cells) < w*h {
		f.Cells = make([]Cell, w*h)
	}
	f.Cells = f.Cells[:w*h]
	for i := range f.Cells {
		f.Cells[i] = Cell{' ', sText}
	}
}

func (f *Frame) text(y int, s string, style uint8) {
	for x := 0; x < len(s) && x < f.W && y < f.H; x++ {
		f.Cells[y*f.W+x] = Cell{s[x], style}
	}
}

// Glyph is the letter that stands for combatant slot i.
func Glyph(i int) byte { return 'A' + byte(i%26) }

// Draw fills f with a w x h picture: the map (a camera window that follows
// combatant me when the terminal is smaller than the map) and the hud lines
// underneath. me is ecs.Nil for spectators.
func Draw(f *Frame, v *world.View, me ecs.Entity, w, h int, hud ...string) {
	f.resize(w, h)
	vw, vh := min(w, v.Map.W), min(h-len(hud), v.Map.H)
	if vw <= 0 || vh <= 0 {
		return
	}
	cx, cy := (v.Zone.X0+v.Zone.X1)/2, (v.Zone.Y0+v.Zone.Y1)/2
	for _, c := range v.Combatants {
		if c.ID == me && c.HP > 0 {
			cx, cy = c.X, c.Y
		}
	}
	ox := max(0, min(cx-vw/2, v.Map.W-vw))
	oy := max(0, min(cy-vh/2, v.Map.H-vh))
	put := func(x, y int, c Cell) {
		if x -= ox; x >= 0 && x < vw {
			if y -= oy; y >= 0 && y < vh {
				f.Cells[y*w+x] = c
			}
		}
	}
	for y := range vh {
		for x := range vw {
			c := Cell{'.', sFloor}
			if v.Map.At(x+ox, y+oy) == world.Wall {
				c = Cell{'#', sWall}
			}
			if !v.Zone.Contains(x+ox, y+oy) {
				c.Style = sOutside
			}
			f.Cells[y*w+x] = c
		}
	}
	for _, p := range v.Pickups {
		put(p.X, p.Y, pickupCells[p.Kind])
	}
	for _, p := range v.Projectiles {
		put(p.X, p.Y, Cell{'*', sProjectile})
	}
	for i, c := range v.Combatants {
		switch {
		case c.HP <= 0:
		case c.ID == me:
			put(c.X, c.Y, Cell{'@', sMe})
		default:
			put(c.X, c.Y, Cell{Glyph(i), sEnemy})
		}
	}
	for i, line := range hud {
		f.text(vh+i, line, sHUD)
	}
}

// Status is the standard first HUD line for viewer me.
func Status(v *world.View, me ecs.Entity) string {
	alive := 0
	who := "SPECTATING"
	for _, c := range v.Combatants {
		if c.HP > 0 {
			alive++
		}
		if c.ID != me {
			continue
		}
		who = "DEAD"
		if c.HP > 0 {
			dash := "ready"
			if c.DashCD > 0 {
				dash = fmt.Sprintf("%.1fs", float64(c.DashCD)/world.TickRate)
			}
			who = fmt.Sprintf("HP %3d  SHIELD %2d  AMMO %2d  DASH %-5s  KILLS %d", c.HP, c.Shield, c.Ammo, dash, c.Kills)
		}
	}
	zone := "final"
	if v.ShrinkIn > 0 {
		zone = fmt.Sprintf("%.1fs", float64(v.ShrinkIn)/world.TickRate)
	}
	return fmt.Sprintf("%s | alive %d/%d | zone %-5s | %2ds", who, alive, len(v.Combatants), zone, (world.MaxTicks-int(v.Tick))/world.TickRate)
}

func moveTo(dst []byte, x, y int) []byte {
	dst = append(dst, "\x1b["...)
	dst = strconv.AppendInt(dst, int64(y+1), 10)
	dst = append(dst, ';')
	dst = strconv.AppendInt(dst, int64(x+1), 10)
	return append(dst, 'H')
}

// Full appends a complete redraw of f to dst.
func Full(dst []byte, f *Frame) []byte {
	style := uint8(255)
	for y := range f.H {
		dst = moveTo(dst, 0, y)
		for _, c := range f.Cells[y*f.W : (y+1)*f.W] {
			if c.Style != style {
				style = c.Style
				dst = append(dst, sgr[style]...)
			}
			dst = append(dst, c.Ch)
		}
	}
	return dst
}

// Diff appends only the cells of next that differ from prev. If the sizes
// differ (first frame, or the terminal was resized) it clears and redraws.
func Diff(dst []byte, prev, next *Frame) []byte {
	if prev.W != next.W || prev.H != next.H {
		return Full(append(dst, "\x1b[2J"...), next)
	}
	style := uint8(255)
	curX, curY := -1, -1 // where the terminal cursor is after our last write
	for i, c := range next.Cells {
		if c == prev.Cells[i] {
			continue
		}
		x, y := i%next.W, i/next.W
		if x != curX || y != curY {
			dst = moveTo(dst, x, y)
		}
		if c.Style != style {
			style = c.Style
			dst = append(dst, sgr[style]...)
		}
		dst = append(dst, c.Ch)
		curX, curY = x+1, y
	}
	return dst
}

// Text renders f as newline-separated lines for programs that own the cursor themselves (Bubble Tea).
func Text(f *Frame) string {
	var dst []byte
	style := uint8(255)
	for y := range f.H {
		for _, c := range f.Cells[y*f.W : (y+1)*f.W] {
			if c.Style != style {
				style = c.Style
				dst = append(dst, sgr[style]...)
			}
			dst = append(dst, c.Ch)
		}
		if y < f.H-1 {
			dst = append(dst, '\n')
		}
	}
	return string(append(dst, sgr[sText]...))
}

// Message fills f with centred lines of text, for screens without a map (the lobby).
func Message(f *Frame, w, h int, lines ...string) {
	f.resize(w, h)
	for i, line := range lines {
		y := (h-len(lines))/2 + i
		if y < 0 || y >= h {
			continue
		}
		for j := 0; j < len(line); j++ {
			if x := (w-len(line))/2 + j; x >= 0 && x < w {
				f.Cells[y*w+x] = Cell{line[j], sHUD}
			}
		}
	}
}
