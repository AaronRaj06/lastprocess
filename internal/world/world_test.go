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
				inputs[i] = Input{e, Action(in.IntN(int(numActions)))}
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
			inputs[i] = Input{e, Action(r.IntN(int(numActions)))}
		}
		s.Step(inputs)
	}
}
