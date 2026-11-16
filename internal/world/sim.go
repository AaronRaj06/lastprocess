// Package world is the pure, deterministic simulation. It must never import
// time, I/O, networking, rendering or storage, and never use the global RNG.
// Given the same Config and the same inputs per tick, state is byte-identical.
package world

import (
	"cmp"
	"encoding/binary"
	"hash/fnv"
	"math/rand/v2"
	"slices"

	"github.com/AaronRaj06/lastprocess/internal/ecs"
)

// TickRate is the fixed simulation rate. The sim never reads the clock; callers pace Step.
const TickRate = 20

// Dir is a cardinal direction.
type Dir uint8

const (
	North Dir = iota
	East
	South
	West
)

var dirDelta = [4][2]int{{0, -1}, {1, 0}, {0, 1}, {-1, 0}}

// Delta returns the unit step for d.
func (d Dir) Delta() (dx, dy int) { return dirDelta[d][0], dirDelta[d][1] }

// Action is the single intent a combatant submits per tick.
type Action uint8

const (
	Stay Action = iota
	MoveN
	MoveE
	MoveS
	MoveW
	ShootN // Shoot and Dash only set facing until Phase 3.
	ShootE
	ShootS
	ShootW
	DashN
	DashE
	DashS
	DashW
	numActions
)

// Valid reports whether a is a defined action. Inputs are checked at the boundary.
func (a Action) Valid() bool { return a < numActions }

// Dir returns the direction carried by a, or false for Stay.
func (a Action) Dir() (Dir, bool) {
	if a == Stay || !a.Valid() {
		return 0, false
	}
	return Dir((a - MoveN) % 4), true
}

// IsMove reports whether a is a move action.
func (a Action) IsMove() bool { return a >= MoveN && a <= MoveW }

// Input is one combatant's intent for one tick. Humans, bots and replays all feed Step the same way.
type Input struct {
	Combatant ecs.Entity
	Action    Action
}

// Position is a grid coordinate.
type Position struct{ X, Y int }

// Combatant is per-process state.
type Combatant struct {
	HP     int
	Facing Dir
}

// StartHP is a combatant's spawn HP.
const StartHP = 100

// Config fully determines a match together with its inputs.
type Config struct {
	Seed          uint64
	Width, Height int // default 80x40 when zero
}

// Sim is one match's state.
type Sim struct {
	Tick uint64
	Map  *Grid

	pcg        *rand.PCG
	rng        *rand.Rand
	ents       *ecs.World
	pos        *ecs.Store[Position]
	combatants *ecs.Store[Combatant]
	occ        []ecs.Entity // scratch occupancy grid, rebuilt each Step
	mapHash    uint64
}

// New creates a match. The map is generated from the seed.
func New(cfg Config) *Sim {
	if cfg.Width == 0 {
		cfg.Width = 80
	}
	if cfg.Height == 0 {
		cfg.Height = 40
	}
	pcg := rand.NewPCG(cfg.Seed, cfg.Seed^0x9E3779B97F4A7C15)
	rng := rand.New(pcg)
	ents := ecs.NewWorld()
	s := &Sim{
		Map:        Generate(rng, cfg.Width, cfg.Height),
		pcg:        pcg,
		rng:        rng,
		ents:       ents,
		pos:        ecs.NewStore[Position](ents),
		combatants: ecs.NewStore[Combatant](ents),
	}
	s.occ = make([]ecs.Entity, len(s.Map.Cells))
	h := fnv.New64a()
	for _, c := range s.Map.Cells {
		h.Write([]byte{byte(c)})
	}
	s.mapHash = h.Sum64()
	return s
}

// Spawn adds a combatant on a random free floor cell. Returns ecs.Nil if the map is full.
func (s *Sim) Spawn() ecs.Entity {
	s.rebuildOcc()
	var free []int
	for i, c := range s.Map.Cells {
		if c == Floor && s.occ[i] == ecs.Nil {
			free = append(free, i)
		}
	}
	if len(free) == 0 {
		return ecs.Nil
	}
	i := free[s.rng.IntN(len(free))]
	return s.spawnAt(i%s.Map.W, i/s.Map.W)
}

func (s *Sim) spawnAt(x, y int) ecs.Entity {
	e := s.ents.Create()
	s.pos.Set(e, Position{x, y})
	s.combatants.Set(e, Combatant{HP: StartHP, Facing: South})
	return e
}

func (s *Sim) rebuildOcc() {
	clear(s.occ)
	s.pos.Each(func(e ecs.Entity, p *Position) { s.occ[p.Y*s.Map.W+p.X] = e })
}

// Step advances one tick. Inputs for unknown entities or invalid actions are
// ignored; if a combatant has several inputs the last one wins. Input slice
// order otherwise does not matter: inputs are applied in ascending entity
// order, so on contested cells the lower entity ID moves first.
func (s *Sim) Step(inputs []Input) {
	in := slices.Clone(inputs)
	slices.SortStableFunc(in, func(a, b Input) int { return cmp.Compare(a.Combatant, b.Combatant) })
	s.rebuildOcc()
	for i, inp := range in {
		if i+1 < len(in) && in[i+1].Combatant == inp.Combatant {
			continue // a later input for the same combatant wins
		}
		c, ok := s.combatants.Get(inp.Combatant)
		if !ok || !inp.Action.Valid() {
			continue
		}
		d, ok := inp.Action.Dir()
		if !ok {
			continue
		}
		c.Facing = d
		if inp.Action.IsMove() {
			s.tryMove(inp.Combatant, d)
		}
	}
	s.Tick++
}

func (s *Sim) tryMove(e ecs.Entity, d Dir) {
	p, _ := s.pos.Get(e)
	dx, dy := d.Delta()
	nx, ny := p.X+dx, p.Y+dy
	if s.Map.At(nx, ny) != Floor || s.occ[ny*s.Map.W+nx] != ecs.Nil {
		return
	}
	s.occ[p.Y*s.Map.W+p.X] = ecs.Nil
	s.occ[ny*s.Map.W+nx] = e
	p.X, p.Y = nx, ny
}

// Hash is an FNV-64a digest of all simulation state, used to prove determinism.
func (s *Sim) Hash() uint64 {
	h := fnv.New64a()
	var buf [8]byte
	put := func(v uint64) {
		binary.LittleEndian.PutUint64(buf[:], v)
		h.Write(buf[:])
	}
	put(s.Tick)
	put(s.mapHash)
	rngState, _ := s.pcg.MarshalBinary() // never errors for PCG
	h.Write(rngState)
	s.combatants.Each(func(e ecs.Entity, c *Combatant) {
		p, _ := s.pos.Get(e)
		put(uint64(e))
		put(uint64(p.X))
		put(uint64(p.Y))
		put(uint64(c.HP))
		put(uint64(c.Facing))
	})
	return h.Sum64()
}

// CombatantView is a read-only copy of one combatant.
type CombatantView struct {
	ID     ecs.Entity
	X, Y   int
	HP     int
	Facing Dir
}

// View is a read-only snapshot for renderers and bots. Map is shared and must not be mutated.
type View struct {
	Tick       uint64
	Map        *Grid
	Combatants []CombatantView // ascending ID
}

// Snapshot copies the current state.
func (s *Sim) Snapshot() View {
	v := View{Tick: s.Tick, Map: s.Map, Combatants: make([]CombatantView, 0, s.combatants.Len())}
	s.combatants.Each(func(e ecs.Entity, c *Combatant) {
		p, _ := s.pos.Get(e)
		v.Combatants = append(v.Combatants, CombatantView{ID: e, X: p.X, Y: p.Y, HP: c.HP, Facing: c.Facing})
	})
	return v
}
