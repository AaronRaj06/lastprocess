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

// Game rules. Changing any of these changes replays, so they are constants, not config.
const (
	StartHP          = 100
	StartAmmo        = 10
	MaxAmmo          = 20
	MaxShield        = 50
	ShootCooldown    = 5  // ticks between shots
	DashCooldown     = 40 // ticks between dashes
	DashRange        = 3  // cells
	ProjectileSpeed  = 2  // cells per tick
	ProjectileDamage = 20
	ZoneDamage       = 1    // HP per tick outside the zone; ignores shield
	ShrinkEvery      = 60   // ticks between zone shrink steps
	MaxTicks         = 1800 // hard cap (90 s); highest HP wins
	PickupEvery      = 40   // ticks between pickup spawns
	MaxPickups       = 12
	HealthAmount     = 30
	AmmoAmount       = 5
	ShieldAmount     = 25
)

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
	ShootN
	ShootE
	ShootS
	ShootW
	DashN
	DashE
	DashS
	DashW
	NumActions
)

var actionNames = [NumActions]string{
	"stay", "move_n", "move_e", "move_s", "move_w",
	"shoot_n", "shoot_e", "shoot_s", "shoot_w", "dash_n", "dash_e", "dash_s", "dash_w",
}

// String is the action's name in the bot API, e.g. "shoot_e".
func (a Action) String() string {
	if !a.Valid() {
		return "invalid"
	}
	return actionNames[a]
}

// ParseAction is the inverse of String.
func ParseAction(s string) (Action, bool) {
	for i, n := range actionNames {
		if n == s {
			return Action(i), true
		}
	}
	return Stay, false
}

// Valid reports whether a is a defined action. Inputs are checked at the boundary.
func (a Action) Valid() bool { return a < NumActions }

// Dir returns the direction carried by a, or false for Stay.
func (a Action) Dir() (Dir, bool) {
	if a == Stay || !a.Valid() {
		return 0, false
	}
	return Dir((a - MoveN) % 4), true
}

// IsMove reports whether a is a move action.
func (a Action) IsMove() bool { return a >= MoveN && a <= MoveW }

// IsShoot reports whether a is a shoot action.
func (a Action) IsShoot() bool { return a >= ShootN && a <= ShootW }

// IsDash reports whether a is a dash action.
func (a Action) IsDash() bool { return a >= DashN && a <= DashW }

// Input is one combatant's intent for one tick. Humans, bots and replays all feed Step the same way.
type Input struct {
	Combatant ecs.Entity
	Action    Action
}

// Position is a grid coordinate.
type Position struct{ X, Y int }

// Combatant is per-process state. HP 0 means dead; the entity stays so results can be read.
type Combatant struct {
	HP, Ammo, Shield int
	Facing           Dir
	ShootCD, DashCD  int // ticks until the action is available again
	Dealt, Kills     int // damage dealt and kills, for results and RL reward
}

// Projectile flies in a straight line until it hits a wall or a combatant other than its owner.
type Projectile struct {
	Dir   Dir
	Owner ecs.Entity
}

// PickupKind is what a pickup grants.
type PickupKind uint8

const (
	Health PickupKind = iota
	Ammo
	Shield
	numPickupKinds
)

// Rect is an inclusive rectangle of cells.
type Rect struct{ X0, Y0, X1, Y1 int }

// Contains reports whether (x, y) is inside r.
func (r Rect) Contains(x, y int) bool { return x >= r.X0 && x <= r.X1 && y >= r.Y0 && y <= r.Y1 }

// zoneSteps is how many shrink steps a w×h map takes before the zone stops at about 12×6.
func zoneSteps(w, h int) int { return max(0, min((w-12)/4, (h-6)/2)) }

// ZoneAt is the safe zone at a tick. It is a pure function of time, so it needs no state:
// each step insets the zone by 2 columns and 1 row per side.
func ZoneAt(w, h int, tick uint64) Rect {
	k := min(int(tick/ShrinkEvery), zoneSteps(w, h))
	return Rect{2 * k, k, w - 1 - 2*k, h - 1 - k}
}

// Config fully determines a match together with its inputs.
type Config struct {
	Seed          uint64
	Width, Height int // default 80x40 when zero
}

// Sim is one match's state.
type Sim struct {
	Tick uint64
	Map  *Grid

	pcg         *rand.PCG
	rng         *rand.Rand
	ents        *ecs.World
	pos         *ecs.Store[Position]
	combatants  *ecs.Store[Combatant]
	projectiles *ecs.Store[Projectile]
	pickups     *ecs.Store[PickupKind]
	occ         []ecs.Entity // live combatant per cell; kept current outside Step
	scratch     []ecs.Entity
	mapHash     uint64
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
		Map:         Generate(rng, cfg.Width, cfg.Height),
		pcg:         pcg,
		rng:         rng,
		ents:        ents,
		pos:         ecs.NewStore[Position](ents),
		combatants:  ecs.NewStore[Combatant](ents),
		projectiles: ecs.NewStore[Projectile](ents),
		pickups:     ecs.NewStore[PickupKind](ents),
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
	s.combatants.Set(e, Combatant{HP: StartHP, Ammo: StartAmmo, Facing: South})
	s.occ[y*s.Map.W+x] = e
	return e
}

func (s *Sim) rebuildOcc() {
	clear(s.occ)
	s.combatants.Each(func(e ecs.Entity, c *Combatant) {
		if c.HP > 0 {
			p, _ := s.pos.Get(e)
			s.occ[p.Y*s.Map.W+p.X] = e
		}
	})
}

// Zone is the current safe zone.
func (s *Sim) Zone() Rect { return ZoneAt(s.Map.W, s.Map.H, s.Tick) }

// ShrinkIn is the number of ticks until the zone next shrinks, or 0 once it has stopped.
func (s *Sim) ShrinkIn() int {
	if int(s.Tick/ShrinkEvery) >= zoneSteps(s.Map.W, s.Map.H) {
		return 0
	}
	return ShrinkEvery - int(s.Tick%ShrinkEvery)
}

func (s *Sim) alive() (n, total int) {
	s.combatants.Each(func(_ ecs.Entity, c *Combatant) {
		total++
		if c.HP > 0 {
			n++
		}
	})
	return n, total
}

// Done reports whether the match is over: one or no combatants left, or the tick cap reached.
func (s *Sim) Done() bool {
	n, total := s.alive()
	return s.Tick >= MaxTicks || n == 0 || (total > 1 && n == 1)
}

// Winner is the live combatant with the most HP once the match is done; ties go
// to the lower ID. It is ecs.Nil while the match runs or if nobody survived.
func (s *Sim) Winner() ecs.Entity {
	if !s.Done() {
		return ecs.Nil
	}
	best, bestHP := ecs.Nil, 0
	s.combatants.Each(func(e ecs.Entity, c *Combatant) {
		if c.HP > bestHP {
			best, bestHP = e, c.HP
		}
	})
	return best
}

// Step advances one tick. Inputs for unknown or dead combatants and invalid
// actions are ignored; if a combatant has several inputs the last one wins.
// Input slice order otherwise does not matter: inputs are applied in ascending
// entity order, so on contested cells the lower entity ID moves first.
// Phases run in a fixed order: actions, projectiles, pickups, zone, pickup spawn.
func (s *Sim) Step(inputs []Input) {
	in := slices.Clone(inputs)
	slices.SortStableFunc(in, func(a, b Input) int { return cmp.Compare(a.Combatant, b.Combatant) })
	s.rebuildOcc()
	s.combatants.Each(func(_ ecs.Entity, c *Combatant) {
		c.ShootCD = max(0, c.ShootCD-1)
		c.DashCD = max(0, c.DashCD-1)
	})
	for i, inp := range in {
		if i+1 < len(in) && in[i+1].Combatant == inp.Combatant {
			continue // a later input for the same combatant wins
		}
		s.act(inp.Combatant, inp.Action)
	}
	s.stepProjectiles()
	s.collectPickups()
	z := s.Zone()
	s.combatants.Each(func(e ecs.Entity, c *Combatant) {
		if p, _ := s.pos.Get(e); c.HP > 0 && !z.Contains(p.X, p.Y) {
			c.HP -= ZoneDamage
			if c.HP <= 0 {
				c.HP = 0
				s.occ[p.Y*s.Map.W+p.X] = ecs.Nil
			}
		}
	})
	if s.Tick%PickupEvery == 0 && s.pickups.Len() < MaxPickups {
		s.spawnPickup(z)
	}
	s.Tick++
}

func (s *Sim) act(e ecs.Entity, a Action) {
	c, ok := s.combatants.Get(e)
	if !ok || c.HP <= 0 {
		return
	}
	d, ok := a.Dir()
	if !ok {
		return
	}
	c.Facing = d
	switch {
	case a.IsMove():
		s.tryMove(e, d)
	case a.IsDash() && c.DashCD == 0:
		c.DashCD = DashCooldown
		for range DashRange {
			if !s.tryMove(e, d) {
				break
			}
		}
	case a.IsShoot() && c.ShootCD == 0 && c.Ammo > 0:
		c.Ammo--
		c.ShootCD = ShootCooldown
		p, _ := s.pos.Get(e)
		at := *p // Set below may move the pos store's backing array
		pr := s.ents.Create()
		s.pos.Set(pr, at) // starts on the shooter and flies out in stepProjectiles
		s.projectiles.Set(pr, Projectile{Dir: d, Owner: e})
	}
}

func (s *Sim) tryMove(e ecs.Entity, d Dir) bool {
	p, _ := s.pos.Get(e)
	dx, dy := d.Delta()
	nx, ny := p.X+dx, p.Y+dy
	if s.Map.At(nx, ny) != Floor || s.occ[ny*s.Map.W+nx] != ecs.Nil {
		return false
	}
	s.occ[p.Y*s.Map.W+p.X] = ecs.Nil
	s.occ[ny*s.Map.W+nx] = e
	p.X, p.Y = nx, ny
	return true
}

// stepProjectiles moves each projectile cell by cell. The hit check runs before
// the first move too, so a combatant that walked onto a projectile is hit and
// nothing can tunnel through.
func (s *Sim) stepProjectiles() {
	spent := s.scratch[:0]
	s.projectiles.Each(func(e ecs.Entity, pr *Projectile) {
		p, _ := s.pos.Get(e)
		dx, dy := pr.Dir.Delta()
		for step := 0; step <= ProjectileSpeed; step++ {
			if step > 0 {
				if s.Map.At(p.X+dx, p.Y+dy) != Floor {
					spent = append(spent, e)
					return
				}
				p.X, p.Y = p.X+dx, p.Y+dy
			}
			if t := s.occ[p.Y*s.Map.W+p.X]; t != ecs.Nil && t != pr.Owner {
				s.hit(t, pr.Owner)
				spent = append(spent, e)
				return
			}
		}
	})
	for _, e := range spent {
		s.ents.Destroy(e)
	}
	s.scratch = spent
}

func (s *Sim) hit(target, owner ecs.Entity) {
	c, _ := s.combatants.Get(target)
	absorbed := min(c.Shield, ProjectileDamage)
	c.Shield -= absorbed
	dmg := min(c.HP, ProjectileDamage-absorbed)
	c.HP -= dmg
	o, _ := s.combatants.Get(owner)
	o.Dealt += dmg + absorbed
	if c.HP == 0 {
		o.Kills++
		p, _ := s.pos.Get(target)
		s.occ[p.Y*s.Map.W+p.X] = ecs.Nil
	}
}

func (s *Sim) collectPickups() {
	taken := s.scratch[:0]
	s.pickups.Each(func(e ecs.Entity, k *PickupKind) {
		p, _ := s.pos.Get(e)
		who := s.occ[p.Y*s.Map.W+p.X]
		if who == ecs.Nil {
			return
		}
		c, _ := s.combatants.Get(who)
		switch *k {
		case Health:
			c.HP = min(StartHP, c.HP+HealthAmount)
		case Ammo:
			c.Ammo = min(MaxAmmo, c.Ammo+AmmoAmount)
		case Shield:
			c.Shield = min(MaxShield, c.Shield+ShieldAmount)
		}
		taken = append(taken, e)
	})
	for _, e := range taken {
		s.ents.Destroy(e)
	}
	s.scratch = taken
}

// spawnPickup tries a few random cells inside the zone and gives up if none is free.
func (s *Sim) spawnPickup(z Rect) {
	for range 20 {
		x := z.X0 + s.rng.IntN(z.X1-z.X0+1)
		y := z.Y0 + s.rng.IntN(z.Y1-z.Y0+1)
		free := s.Map.At(x, y) == Floor && s.occ[y*s.Map.W+x] == ecs.Nil
		s.pickups.Each(func(e ecs.Entity, _ *PickupKind) {
			if p, _ := s.pos.Get(e); p.X == x && p.Y == y {
				free = false
			}
		})
		if free {
			e := s.ents.Create()
			s.pos.Set(e, Position{x, y})
			s.pickups.Set(e, PickupKind(s.rng.IntN(int(numPickupKinds))))
			return
		}
	}
}

// Hash is an FNV-64a digest of all simulation state, used to prove determinism.
func (s *Sim) Hash() uint64 {
	h := fnv.New64a()
	var buf [8]byte
	put := func(vs ...int) {
		for _, v := range vs {
			binary.LittleEndian.PutUint64(buf[:], uint64(v))
			h.Write(buf[:])
		}
	}
	put(int(s.Tick), int(s.mapHash))
	rngState, _ := s.pcg.MarshalBinary() // never errors for PCG
	h.Write(rngState)
	s.combatants.Each(func(e ecs.Entity, c *Combatant) {
		p, _ := s.pos.Get(e)
		put(int(e), p.X, p.Y, c.HP, c.Ammo, c.Shield, int(c.Facing), c.ShootCD, c.DashCD, c.Dealt, c.Kills)
	})
	s.projectiles.Each(func(e ecs.Entity, pr *Projectile) {
		p, _ := s.pos.Get(e)
		put(int(e), p.X, p.Y, int(pr.Dir), int(pr.Owner))
	})
	s.pickups.Each(func(e ecs.Entity, k *PickupKind) {
		p, _ := s.pos.Get(e)
		put(int(e), p.X, p.Y, int(*k))
	})
	return h.Sum64()
}

// CombatantView is a read-only copy of one combatant.
type CombatantView struct {
	ID   ecs.Entity
	X, Y int
	Combatant
}

// PickupView is a read-only copy of one pickup.
type PickupView struct {
	X, Y int
	Kind PickupKind
}

// View is a read-only snapshot for renderers. Map is shared and must not be mutated.
type View struct {
	Tick        uint64
	Map         *Grid
	Zone        Rect
	ShrinkIn    int
	Combatants  []CombatantView // ascending ID, dead ones included with HP 0
	Projectiles []Position
	Pickups     []PickupView
	Done        bool
	Winner      ecs.Entity
}

// Snapshot copies the current state.
func (s *Sim) Snapshot() View {
	v := View{
		Tick: s.Tick, Map: s.Map, Zone: s.Zone(), ShrinkIn: s.ShrinkIn(), Done: s.Done(), Winner: s.Winner(),
		Combatants: make([]CombatantView, 0, s.combatants.Len()),
	}
	s.combatants.Each(func(e ecs.Entity, c *Combatant) {
		p, _ := s.pos.Get(e)
		v.Combatants = append(v.Combatants, CombatantView{ID: e, X: p.X, Y: p.Y, Combatant: *c})
	})
	s.projectiles.Each(func(e ecs.Entity, _ *Projectile) {
		p, _ := s.pos.Get(e)
		v.Projectiles = append(v.Projectiles, *p)
	})
	s.pickups.Each(func(e ecs.Entity, k *PickupKind) {
		p, _ := s.pos.Get(e)
		v.Pickups = append(v.Pickups, PickupView{p.X, p.Y, *k})
	})
	return v
}
