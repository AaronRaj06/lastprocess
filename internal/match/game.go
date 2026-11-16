// Package match runs matches: Game is one match driven through bot.Controller
// (used by the server, the headless runner and tests); Manager paces live
// matches at 20 Hz and slots humans and bots into them.
package match

import (
	"github.com/AaronRaj06/lastprocess/internal/bot"
	"github.com/AaronRaj06/lastprocess/internal/ecs"
	"github.com/AaronRaj06/lastprocess/internal/replay"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

// Slot is one combatant: who it is and what drives it.
type Slot struct {
	Name string
	Kind string // "human", "baseline", "random", "rl", "lua"
	Ctl  bot.Controller
}

// Game is one match. It is not safe for concurrent use.
type Game struct {
	Sim   *world.Sim
	Slots []Slot
	IDs   []ecs.Entity // IDs[i] is Slots[i]'s combatant
	Log   replay.Log

	obs     world.Observation
	in      []world.Input
	actions []world.Action
}

// NewGame creates the sim from seed and spawns one combatant per slot, in order.
func NewGame(seed uint64, slots []Slot) *Game {
	g := &Game{Sim: world.New(world.Config{Seed: seed}), Slots: slots}
	g.Log.Seed = seed
	for _, s := range slots {
		g.IDs = append(g.IDs, g.Sim.Spawn())
		g.Log.Names = append(g.Log.Names, s.Name)
	}
	g.in = make([]world.Input, len(slots))
	g.actions = make([]world.Action, len(slots))
	return g
}

// Tick asks every controller for an action, steps the sim and records the inputs.
// Dead combatants are still asked (with Alive=false) so controllers see the end.
func (g *Game) Tick() {
	for i, s := range g.Slots {
		g.Sim.Observe(g.IDs[i], &g.obs)
		a := s.Ctl.Act(&g.obs)
		if !a.Valid() || !g.obs.Alive {
			a = world.Stay
		}
		g.actions[i] = a
		g.in[i] = world.Input{Combatant: g.IDs[i], Action: a}
	}
	g.Sim.Step(g.in)
	g.Log.Record(g.actions)
	g.Log.Hash = g.Sim.Hash()
}

// Run ticks until the match is done and returns the winning slot, or -1 for no survivor.
func (g *Game) Run() int {
	for !g.Sim.Done() {
		g.Tick()
	}
	return g.Winner()
}

// Winner is the winning slot index, or -1 while running or if nobody survived.
func (g *Game) Winner() int {
	w := g.Sim.Winner()
	for i, id := range g.IDs {
		if id == w && w != ecs.Nil {
			return i
		}
	}
	return -1
}
