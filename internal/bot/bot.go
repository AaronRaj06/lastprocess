// Package bot defines the one interface every combatant is driven through,
// plus the native bots. The sim cannot tell a human, an RL policy, a Lua
// script or a native bot apart: each is a Controller.
package bot

import (
	"math/rand/v2"

	"github.com/AaronRaj06/lastprocess/internal/world"
)

// Controller picks one action per tick from an observation.
// Implementations must be deterministic given their seed and observations.
type Controller interface {
	Act(o *world.Observation) world.Action
}

// Func adapts a function to Controller.
type Func func(o *world.Observation) world.Action

// Act calls f.
func (f Func) Act(o *world.Observation) world.Action { return f(o) }

func newRNG(seed uint64) *rand.Rand { return rand.New(rand.NewPCG(seed, 0xB07)) }

// NewRandom returns a bot that picks a uniformly random action every tick.
func NewRandom(seed uint64) Controller {
	rng := newRNG(seed)
	return Func(func(*world.Observation) world.Action { return world.Action(rng.IntN(int(world.NumActions))) })
}

// Baseline is the scripted reference bot: stay in the zone, line up with the
// nearest visible enemy, shoot when aligned, otherwise drift to the zone centre.
type Baseline struct{ rng *rand.Rand }

// NewBaseline returns a Baseline whose random fallbacks come from seed.
func NewBaseline(seed uint64) *Baseline { return &Baseline{newRNG(seed)} }

// Act implements Controller.
func (b *Baseline) Act(o *world.Observation) world.Action {
	if !o.Alive {
		return world.Stay
	}
	if o.ZoneDist > 1 && o.NEnemies > 0 && o.Ammo > 0 {
		e := o.Enemies[0]
		if e.DX == 0 || e.DY == 0 {
			if o.ShootCD > 0 {
				return world.Stay
			}
			return world.ShootN + world.Action(toward(e.DX, e.DY))
		}
		// Close the shorter gap first to get aligned.
		if abs(e.DX) <= abs(e.DY) {
			return b.move(o, e.DX, 0)
		}
		return b.move(o, 0, e.DY)
	}
	return b.move(o, -o.ZoneDX, -o.ZoneDY)
}

// move steps along the larger axis of (dx, dy), then the other, then randomly if both are blocked.
func (b *Baseline) move(o *world.Observation, dx, dy int) world.Action {
	tries := [2][2]int{{dx, 0}, {0, dy}}
	if abs(dy) > abs(dx) {
		tries[0], tries[1] = tries[1], tries[0]
	}
	for _, t := range tries {
		if t == [2]int{} {
			continue
		}
		d := toward(t[0], t[1])
		sx, sy := d.Delta()
		if c := o.View[(sy+world.ViewSize/2)*world.ViewSize+sx+world.ViewSize/2]; c != world.CellWall && c != world.CellEnemy {
			return world.MoveN + world.Action(d)
		}
	}
	return world.MoveN + world.Action(b.rng.IntN(4))
}

// toward returns the direction of the larger component of (dx, dy).
func toward(dx, dy int) world.Dir {
	switch {
	case abs(dx) >= abs(dy) && dx > 0:
		return world.East
	case abs(dx) >= abs(dy) && dx < 0:
		return world.West
	case dy > 0:
		return world.South
	}
	return world.North
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
