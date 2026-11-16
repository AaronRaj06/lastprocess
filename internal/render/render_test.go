package render

import (
	"strings"
	"testing"

	"github.com/AaronRaj06/lastprocess/internal/ecs"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

// script drives 8 combatants with a fixed pseudo-random pattern; no bot package needed here.
func play(seed uint64, each func(v *world.View, me ecs.Entity)) {
	s := world.New(world.Config{Seed: seed})
	var ids []ecs.Entity
	for range 8 {
		ids = append(ids, s.Spawn())
	}
	in := make([]world.Input, len(ids))
	x := seed*2654435761 + 1
	for !s.Done() {
		for i, id := range ids {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			in[i] = world.Input{Combatant: id, Action: world.Action(x % uint64(world.NumActions))}
		}
		s.Step(in)
		v := s.Snapshot()
		each(&v, ids[0])
	}
}

func rows(f *Frame) []string {
	out := make([]string, f.H)
	for y := range f.H {
		var b strings.Builder
		for _, c := range f.Cells[y*f.W : (y+1)*f.W] {
			b.WriteByte(c.Ch)
		}
		out[y] = b.String()
	}
	return out
}

// Applying the diff stream to a terminal must show exactly the frames that
// were drawn, for a full-size terminal and for a small scrolling one.
func TestDiffStreamReproducesFrames(t *testing.T) {
	for _, size := range [][2]int{{80, 43}, {40, 20}, {120, 50}} {
		w, h := size[0], size[1]
		scr := NewScreen(w, h)
		var prev, cur Frame
		var out []byte
		ticks := 0
		play(3, func(v *world.View, me ecs.Entity) {
			Draw(&cur, v, me, w, h, Status(v, me), "second line")
			out = Diff(out[:0], &prev, &cur)
			_, _ = scr.Write(out)
			for y, want := range rows(&cur) {
				if string(scr.Rows[y]) != want {
					t.Fatalf("%dx%d tick %d row %d:\n got %q\nwant %q", w, h, v.Tick, y, scr.Rows[y], want)
				}
			}
			prev, cur = cur, prev
			ticks++
		})
		if ticks == 0 {
			t.Fatal("no frames")
		}
	}
}

func TestDrawShowsPlayerAndHUD(t *testing.T) {
	var f Frame
	play(1, func(v *world.View, me ecs.Entity) {
		if v.Tick != 1 {
			return
		}
		Draw(&f, v, me, 80, 42, Status(v, me))
		all := strings.Join(rows(&f), "\n")
		if strings.Count(all, "@") != 1 || !strings.Contains(all, "HP 100") || !strings.Contains(all, "alive 8/8") {
			t.Fatalf("frame:\n%s", all)
		}
		if got := Text(&f); !strings.Contains(got, "@") || strings.Count(got, "\n") != 41 {
			t.Fatal("Text output malformed")
		}
		Draw(&f, v, ecs.Nil, 80, 42, Status(v, ecs.Nil))
		if all := strings.Join(rows(&f), "\n"); strings.Contains(all, "@") || !strings.Contains(all, "SPECTATING") {
			t.Fatal("spectator frame wrong")
		}
		Message(&f, 30, 5, "hello", "a line that is much longer than the frame is wide")
		if !strings.Contains(rows(&f)[1], "hello") {
			t.Fatalf("message: %q", rows(&f))
		}
		Draw(&f, v, me, 3, 1, "x") // degenerate sizes must not panic
	})
}

// BenchmarkFrameBytes reports the wire size of one frame for a player on a
// full 80x43 terminal: a complete redraw against the diff. Averaged over whole matches.
func BenchmarkFrameBytes(b *testing.B) {
	var full, diff, frames int
	for i := 0; b.Loop(); i++ {
		var prev, cur Frame
		var out []byte
		play(uint64(i), func(v *world.View, me ecs.Entity) {
			Draw(&cur, v, me, 80, 43, Status(v, me), "wasd move  arrows shoot  WASD dash  q quit", "")
			out = Diff(out[:0], &prev, &cur)
			diff += len(out)
			full += len(Full(out[:0], &cur))
			prev, cur = cur, prev
			frames++
		})
	}
	b.ReportMetric(float64(full)/float64(frames), "full-B/frame")
	b.ReportMetric(float64(diff)/float64(frames), "diff-B/frame")
	b.ReportMetric(100*(1-float64(diff)/float64(full)), "%-saved")
}
