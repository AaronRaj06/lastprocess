package match

import (
	"testing"

	"github.com/AaronRaj06/lastprocess/internal/replay"
)

func lineup(t testing.TB, s string) []string {
	t.Helper()
	l, err := ParseLineup(s)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// The CI determinism gate: full bot matches on many seeds, run twice, must
// hash identically after every tick, and the recorded log must replay to the
// same final hash after an encode/decode round trip.
func TestMatchesDeterministicAndReplay(t *testing.T) {
	const seeds = 50
	l := lineup(t, "baseline*4,random*4")
	for seed := range uint64(seeds) {
		a, _ := NewGameFromLineup(seed, l)
		b, _ := NewGameFromLineup(seed, l)
		for !a.Sim.Done() {
			a.Tick()
			b.Tick()
			if a.Sim.Hash() != b.Sim.Hash() {
				t.Fatalf("seed %d tick %d: hash diverged", seed, a.Sim.Tick)
			}
		}
		log, err := replay.Decode(a.Log.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if err := log.Verify(); err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		log.Actions[len(log.Actions)/2] ^= 1 // a tampered log must not verify
		if log.Verify() == nil && a.Sim.Tick > 10 {
			t.Logf("seed %d: flipped action had no effect (possible for a dead combatant)", seed)
		}
	}
	t.Logf("%d seeds replayed bit-identically", seeds)
}

func TestMatchEndsWithWinner(t *testing.T) {
	wins := 0
	for seed := range uint64(40) {
		g, _ := NewGameFromLineup(seed, lineup(t, "baseline,random*7"))
		if w := g.Run(); w == 0 {
			wins++
		}
		if g.Sim.Tick == 0 || g.Log.Ticks() != int(g.Sim.Tick) {
			t.Fatalf("seed %d: ticks %d logged %d", seed, g.Sim.Tick, g.Log.Ticks())
		}
	}
	if wins < 10 { // 1 of 8 by chance would be 5
		t.Fatalf("baseline won only %d of 40 against 7 random bots", wins)
	}
}

func TestParseLineupRejectsBadInput(t *testing.T) {
	for _, s := range []string{"", "baseline", "nope*2", "random*0", "random*17"} {
		if _, err := ParseLineup(s); err == nil {
			t.Errorf("%q accepted", s)
		}
	}
}

func BenchmarkMatch8(b *testing.B) {
	l := lineup(b, "baseline*8")
	ticks := 0
	for i := 0; b.Loop(); i++ {
		g, _ := NewGameFromLineup(uint64(i), l)
		g.Run()
		ticks += int(g.Sim.Tick)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(ticks), "ns/tick")
}
