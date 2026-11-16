package replay

import (
	"testing"

	"github.com/AaronRaj06/lastprocess/internal/world"
)

func TestRoundTripVerifyAndTamper(t *testing.T) {
	l := &Log{Seed: 5, Names: []string{"a", "b", "c"}}
	rec := NewPlayer(&Log{Seed: 5, Names: l.Names})
	for tick := range 300 {
		acts := []world.Action{world.Action(tick % 13), world.Action((tick * 7) % 13), world.Action((tick * 3) % 13)}
		l.Record(acts)
	}
	// Produce the reference hash by playing the log once.
	rec.log = l
	for rec.Step() {
	}
	l.Hash = rec.Sim.Hash()

	got, err := Decode(l.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if got.Ticks() != 300 || got.Seed != 5 || len(got.Names) != 3 {
		t.Fatalf("%+v", got)
	}
	if err := got.Verify(); err != nil {
		t.Fatal(err)
	}
	got.Seed++
	if got.Verify() == nil {
		t.Fatal("a log with the wrong seed verified")
	}
	for _, bad := range [][]byte{nil, []byte("junk"), (&Log{}).Encode(), (&Log{Names: []string{"a", "b"}, Actions: []byte{1}}).Encode()} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("decoded malformed log %q", bad)
		}
	}
}
