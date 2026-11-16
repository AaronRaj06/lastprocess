package bot

import (
	"encoding/json"
	"math"
	"os"
	"testing"

	"github.com/AaronRaj06/lastprocess/internal/world"
)

// The Go ONNX runner must reproduce PyTorch's logits for the committed policy.
// testdata/parity.json is written by trainer/train.py at export time.
func TestPolicyMatchesPyTorch(t *testing.T) {
	p, err := EmbeddedPolicy()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/parity.json")
	if err != nil {
		t.Fatal(err)
	}
	var data struct{ Obs, Logits [][]float32 }
	if err := json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	var worst float64
	for i, obs := range data.Obs {
		got := p.Logits(obs)
		if len(got) != int(world.NumActions) {
			t.Fatalf("%d logits", len(got))
		}
		for j, want := range data.Logits[i] {
			worst = max(worst, math.Abs(float64(got[j]-want)))
		}
	}
	if worst > 1e-4 {
		t.Fatalf("max logit error %g", worst)
	}
	t.Logf("%d vectors, max abs logit error %.2g", len(data.Obs), worst)
}

func TestParsePolicyRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, {0xff}, {0x3a, 0x05, 1, 2}, []byte("not an onnx file at all")} {
		if _, err := ParsePolicy(b); err == nil {
			t.Errorf("accepted %q", b)
		}
	}
}

func obsWith(mut func(o *world.Observation)) *world.Observation {
	o := &world.Observation{Alive: true, HP: 100, Ammo: 10, ZoneDist: 10}
	mut(o)
	return o
}

func TestBaselineHeuristics(t *testing.T) {
	b := NewBaseline(1)
	cases := []struct {
		name string
		o    *world.Observation
		want world.Action
	}{
		{"dead", &world.Observation{}, world.Stay},
		{"shoot when aligned", obsWith(func(o *world.Observation) { o.NEnemies, o.Enemies[0] = 1, world.EnemyObs{DX: 4} }), world.ShootE},
		{"wait for cooldown", obsWith(func(o *world.Observation) { o.NEnemies, o.Enemies[0], o.ShootCD = 1, world.EnemyObs{DY: -3}, 2 }), world.Stay},
		{"line up on the short axis", obsWith(func(o *world.Observation) { o.NEnemies, o.Enemies[0] = 1, world.EnemyObs{DX: -1, DY: 5} }), world.MoveW},
		{"back to the zone first", obsWith(func(o *world.Observation) {
			o.NEnemies, o.Enemies[0], o.ZoneDist, o.ZoneDX = 1, world.EnemyObs{DX: 4}, -2, 9
		}), world.MoveW},
		{"no ammo: head for the centre", obsWith(func(o *world.Observation) {
			o.NEnemies, o.Enemies[0], o.Ammo, o.ZoneDY = 1, world.EnemyObs{DX: 4}, 0, -6
		}), world.MoveS},
		{"go around a wall", obsWith(func(o *world.Observation) {
			o.ZoneDX, o.ZoneDY = -9, -2
			o.View[7*world.ViewSize+8] = world.CellWall // east is blocked
		}), world.MoveS},
	}
	for _, c := range cases {
		if got := b.Act(c.o); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	r := NewRandom(1)
	seen := map[world.Action]bool{}
	for range 500 {
		a := r.Act(nil)
		if !a.Valid() {
			t.Fatal("random bot produced an invalid action")
		}
		seen[a] = true
	}
	if len(seen) != int(world.NumActions) {
		t.Fatalf("random bot used %d of %d actions", len(seen), world.NumActions)
	}
}

func BenchmarkPolicyAct(b *testing.B) {
	p, err := EmbeddedPolicy()
	if err != nil {
		b.Skip(err)
	}
	c := p.Controller()
	o := obsWith(func(o *world.Observation) { o.NEnemies, o.Enemies[0] = 1, world.EnemyObs{DX: 2, DY: 1} })
	for b.Loop() {
		c.Act(o)
	}
}

func BenchmarkBaselineAct(b *testing.B) {
	c := NewBaseline(1)
	o := obsWith(func(o *world.Observation) { o.NEnemies, o.Enemies[0] = 1, world.EnemyObs{DX: 2, DY: 1} })
	for b.Loop() {
		c.Act(o)
	}
}
