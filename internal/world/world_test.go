package world

import (
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/AaronRaj06/lastprocess/internal/ecs"
)

func TestMapSameSeedIdentical(t *testing.T) {
	for seed := range uint64(100) {
		a, b := New(Config{Seed: seed}), New(Config{Seed: seed})
		if !slices.Equal(a.Map.Cells, b.Map.Cells) || a.mapHash != b.mapHash {
			t.Fatalf("seed %d: maps differ", seed)
		}
	}
	if New(Config{Seed: 1}).mapHash == New(Config{Seed: 2}).mapHash {
		t.Fatal("different seeds produced the same map")
	}
}

func TestMapConnectedWithWallBorder(t *testing.T) {
	for seed := range uint64(100) {
		g := New(Config{Seed: seed}).Map
		floors, start := 0, -1
		for i, c := range g.Cells {
			x, y := i%g.W, i/g.W
			if c == Floor {
				floors++
				if start < 0 {
					start = i
				}
				if x == 0 || y == 0 || x == g.W-1 || y == g.H-1 {
					t.Fatalf("seed %d: floor on border at %d,%d", seed, x, y)
				}
			}
		}
		if floors < g.W*g.H/10 {
			t.Fatalf("seed %d: only %d floor cells", seed, floors)
		}
		seen := map[int]bool{start: true}
		stack := []int{start}
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			for _, d := range dirDelta {
				x, y := i%g.W+d[0], i/g.W+d[1]
				j := y*g.W + x
				if g.At(x, y) == Floor && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		if len(seen) != floors {
			t.Fatalf("seed %d: reached %d of %d floor cells", seed, len(seen), floors)
		}
	}
}

func TestSpawnOnFreeFloor(t *testing.T) {
	s := New(Config{Seed: 7})
	seen := map[Position]bool{}
	for range 16 {
		e := s.Spawn()
		p, _ := s.pos.Get(e)
		if s.Map.At(p.X, p.Y) != Floor || seen[*p] {
			t.Fatalf("bad spawn at %v", *p)
		}
		seen[*p] = true
	}
}

// openSim returns a sim whose map is an open room, for hand-placed tests.
func openSim() *Sim {
	s := New(Config{Seed: 1, Width: 20, Height: 10})
	for y := range s.Map.H {
		for x := range s.Map.W {
			c := Floor
			if x == 0 || y == 0 || x == s.Map.W-1 || y == s.Map.H-1 {
				c = Wall
			}
			s.Map.set(x, y, c)
		}
	}
	return s
}

func pos(t *testing.T, s *Sim, e ecs.Entity) Position {
	t.Helper()
	p, ok := s.pos.Get(e)
	if !ok {
		t.Fatalf("no position for %v", e)
	}
	return *p
}

func TestMoveAndWallBlocks(t *testing.T) {
	s := openSim()
	e := s.spawnAt(1, 1)
	s.Step([]Input{{e, MoveN}}) // into the border wall
	if got := pos(t, s, e); got != (Position{1, 1}) {
		t.Fatalf("walked into wall: %v", got)
	}
	s.Step([]Input{{e, MoveE}})
	if got := pos(t, s, e); got != (Position{2, 1}) {
		t.Fatalf("move east failed: %v", got)
	}
	c, _ := s.combatants.Get(e)
	if c.Facing != East {
		t.Fatalf("facing = %v", c.Facing)
	}
	s.Step([]Input{{e, Action(200)}, {ecs.Entity(999), MoveE}}) // invalid action, unknown entity
	if got := pos(t, s, e); got != (Position{2, 1}) || s.Tick != 3 {
		t.Fatalf("invalid input changed state: %v tick %d", got, s.Tick)
	}
}

func TestContestedCellLowerIDWins(t *testing.T) {
	s := openSim()
	a := s.spawnAt(1, 1)
	b := s.spawnAt(3, 1)
	s.Step([]Input{{b, MoveW}, {a, MoveE}}) // both target (2,1); slice order must not matter
	if pos(t, s, a) != (Position{2, 1}) || pos(t, s, b) != (Position{3, 1}) {
		t.Fatalf("a=%v b=%v", pos(t, s, a), pos(t, s, b))
	}
}

func TestLastInputPerCombatantWins(t *testing.T) {
	s := openSim()
	e := s.spawnAt(5, 5)
	s.Step([]Input{{e, MoveN}, {e, MoveS}})
	if got := pos(t, s, e); got != (Position{5, 6}) {
		t.Fatalf("got %v", got)
	}
}

// Two sims fed the same seed and inputs must hash identically every tick,
// and input slice order must not matter.
func TestStepDeterministic(t *testing.T) {
	for seed := range uint64(20) {
		a, b := New(Config{Seed: seed}), New(Config{Seed: seed})
		var ids []ecs.Entity
		for range 8 {
			ids = append(ids, a.Spawn())
			b.Spawn()
		}
		in := rand.New(rand.NewPCG(seed, 99))
		for tick := range 1000 {
			inputs := make([]Input, len(ids))
			for i, e := range ids {
				inputs[i] = Input{e, Action(in.IntN(int(NumActions)))}
			}
			shuffled := slices.Clone(inputs)
			in.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
			a.Step(inputs)
			b.Step(shuffled)
			if a.Hash() != b.Hash() {
				t.Fatalf("seed %d tick %d: hash diverged", seed, tick)
			}
		}
	}
}

func BenchmarkStep16(b *testing.B) {
	s := New(Config{Seed: 1})
	var ids []ecs.Entity
	for range 16 {
		ids = append(ids, s.Spawn())
	}
	r := rand.New(rand.NewPCG(1, 2))
	inputs := make([]Input, len(ids))
	for b.Loop() {
		for i, e := range ids {
			inputs[i] = Input{e, Action(r.IntN(int(NumActions)))}
		}
		s.Step(inputs)
	}
}

func comb(s *Sim, e ecs.Entity) *Combatant { c, _ := s.combatants.Get(e); return c }

func TestShootHitsShieldThenHP(t *testing.T) {
	s := openSim()
	a, b := s.spawnAt(2, 2), s.spawnAt(8, 2)
	comb(s, b).Shield = 5
	s.Step([]Input{{a, ShootE}})
	for range 5 {
		s.Step(nil)
	}
	if cb := comb(s, b); cb.HP != StartHP-15 || cb.Shield != 0 {
		t.Fatalf("hp %d shield %d", cb.HP, cb.Shield)
	}
	if ca := comb(s, a); ca.Ammo != StartAmmo-1 || ca.Dealt != ProjectileDamage || s.projectiles.Len() != 0 {
		t.Fatalf("shooter %+v, %d projectiles left", *ca, s.projectiles.Len())
	}
	s.Step([]Input{{a, ShootE}}) // cooldown elapsed during the idle ticks
	s.Step([]Input{{a, ShootE}}) // still cooling down: ignored
	if comb(s, a).Ammo != StartAmmo-2 {
		t.Fatalf("cooldown not enforced: ammo %d", comb(s, a).Ammo)
	}
}

func TestProjectileBlockedByWallAndNoTunnelling(t *testing.T) {
	s := openSim()
	s.Map.set(5, 2, Wall)
	a, b := s.spawnAt(2, 2), s.spawnAt(8, 2)
	s.Step([]Input{{a, ShootE}})
	for range 5 {
		s.Step(nil)
	}
	if comb(s, b).HP != StartHP || s.projectiles.Len() != 0 {
		t.Fatal("projectile passed through a wall")
	}
	// Adjacent target walking into the shot must still be hit.
	s = openSim()
	a, b = s.spawnAt(2, 2), s.spawnAt(4, 2)
	s.Step([]Input{{a, ShootE}, {b, MoveW}})
	if comb(s, b).HP != StartHP-ProjectileDamage {
		t.Fatalf("hp %d", comb(s, b).HP)
	}
}

func TestDashStopsAtObstacleAndCoolsDown(t *testing.T) {
	s := openSim()
	e := s.spawnAt(2, 2)
	s.Step([]Input{{e, DashE}})
	if got := pos(t, s, e); got != (Position{5, 2}) {
		t.Fatalf("dash: %v", got)
	}
	s.Step([]Input{{e, DashE}}) // on cooldown
	if got := pos(t, s, e); got != (Position{5, 2}) {
		t.Fatalf("dash ignored cooldown: %v", got)
	}
	s = openSim()
	e = s.spawnAt(16, 2)
	s.Step([]Input{{e, DashE}})
	if got := pos(t, s, e); got != (Position{18, 2}) {
		t.Fatalf("dash through wall: %v", got)
	}
}

func TestPickupsApplyAndCap(t *testing.T) {
	s := openSim()
	e := s.spawnAt(2, 2)
	comb(s, e).HP = 50
	for i, k := range []PickupKind{Health, Ammo, Shield} {
		p := s.ents.Create()
		s.pos.Set(p, Position{3 + i, 2})
		s.pickups.Set(p, k)
	}
	for range 3 {
		s.Step([]Input{{e, MoveE}})
	}
	if c := comb(s, e); c.HP != 80 || c.Ammo != StartAmmo+AmmoAmount || c.Shield != ShieldAmount {
		t.Fatalf("%+v", *c)
	}
}

func TestZoneShrinksAndKills(t *testing.T) {
	if z := ZoneAt(80, 40, 0); z != (Rect{0, 0, 79, 39}) {
		t.Fatalf("initial zone %v", z)
	}
	if z := ZoneAt(80, 40, MaxTicks); z != (Rect{34, 17, 45, 22}) {
		t.Fatalf("final zone %v", z)
	}
	s := New(Config{Seed: 3})
	a, b := s.Spawn(), s.Spawn()
	for !s.Done() {
		s.Step(nil)
	}
	// Nobody moves, so the zone or the tick cap must end the match.
	if s.Tick > MaxTicks || (comb(s, a).HP > 0 && comb(s, b).HP > 0 && s.Tick < MaxTicks) {
		t.Fatalf("tick %d", s.Tick)
	}
	if w := s.Winner(); w != ecs.Nil && comb(s, w).HP == 0 {
		t.Fatal("dead winner")
	}
}

func TestWinnerIsLastAlive(t *testing.T) {
	s := openSim()
	a, b := s.spawnAt(2, 2), s.spawnAt(4, 2)
	comb(s, b).HP = 20
	if s.Done() || s.Winner() != ecs.Nil {
		t.Fatal("done too early")
	}
	s.Step([]Input{{a, ShootE}})
	if !s.Done() || s.Winner() != a || comb(s, a).Kills != 1 {
		t.Fatalf("done %v winner %v", s.Done(), s.Winner())
	}
}

func TestObserve(t *testing.T) {
	s := openSim()
	a := s.spawnAt(2, 2)
	s.spawnAt(5, 2)
	s.spawnAt(2, 4)
	s.spawnAt(18, 8) // outside the 15x15 view
	var o Observation
	s.Observe(a, &o)
	if !o.Alive || o.NEnemies != 2 || o.Enemies[0] != (EnemyObs{0, 2, South}) || o.Enemies[1] != (EnemyObs{3, 0, South}) {
		t.Fatalf("%+v", o.Enemies)
	}
	at := func(dx, dy int) uint8 { return o.View[(dy+7)*ViewSize+dx+7] }
	if at(3, 0) != CellEnemy || at(-2, 0) != CellWall || at(1, 0) != CellFloor || at(-7, -7) != CellWall {
		t.Fatal("view cells wrong")
	}
	if o.ZoneDist != 2 || o.ShrinkIn != ShrinkEvery {
		t.Fatalf("zone dist %d shrink %d", o.ZoneDist, o.ShrinkIn)
	}
	v := make([]float32, ObsLen)
	o.Vector(v)
	if v[225] != 1 || v[235] != 1 || v[237] != float32(2)/7 || v[243] != 0 {
		t.Fatalf("vector %v", v[225:])
	}
}

func TestVectorRays(t *testing.T) {
	s := openSim()
	a := s.spawnAt(2, 2)
	s.spawnAt(5, 2)
	var o Observation
	s.Observe(a, &o)
	v := make([]float32, ObsLen)
	o.Vector(v)
	rays := v[ObsLen-12:]
	// North: wall after 1 free cell. East: enemy 3 cells away in the line of fire.
	if rays[0] != float32(1)/7 || rays[1] != 0 || rays[4] != float32(5)/7 || rays[9] != float32(1)/7 {
		t.Fatalf("rays %v", rays)
	}
}

func TestThreats(t *testing.T) {
	s := openSim()
	a, b := s.spawnAt(2, 2), s.spawnAt(10, 2)
	s.Step([]Input{{b, ShootW}, {a, ShootE}}) // both shots are now 2 cells out
	var o Observation
	s.Observe(a, &o)
	if o.Threats != [4]int{East: 6} { // b's shot at x=8; a's own shot at x=4 is not a threat
		t.Fatalf("threats %v", o.Threats)
	}
	v := make([]float32, ObsLen)
	o.Vector(v)
	if got := v[ObsLen-12+int(East)*3+2]; got != float32(2)/7 {
		t.Fatalf("threat feature %v", got)
	}
}
