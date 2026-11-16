package ecs

import (
	"fmt"
	"testing"
)

type pos struct{ X, Y int32 }
type vel struct{ DX, DY int32 }

// BenchmarkMovementSystem measures a two-component system (pos += vel) over N
// entities. Reported as entities/s via ReportMetric.
func BenchmarkMovementSystem(b *testing.B) {
	for _, n := range []int{16, 400, 100_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			w := NewWorld()
			ps, vs := NewStore[pos](w), NewStore[vel](w)
			for i := range n {
				e := w.Create()
				ps.Set(e, pos{int32(i), 0})
				vs.Set(e, vel{1, 1})
			}
			b.ResetTimer()
			for b.Loop() {
				vs.Each(func(e Entity, v *vel) {
					if p, ok := ps.Get(e); ok {
						p.X += v.DX
						p.Y += v.DY
					}
				})
			}
			b.ReportMetric(float64(n)*float64(b.N)/b.Elapsed().Seconds(), "entities/s")
		})
	}
}

// BenchmarkSpawnDespawn measures projectile-style churn: create, attach two components, destroy.
func BenchmarkSpawnDespawn(b *testing.B) {
	w := NewWorld()
	ps, vs := NewStore[pos](w), NewStore[vel](w)
	live := make([]Entity, 0, 400)
	for range 400 {
		e := w.Create()
		ps.Set(e, pos{})
		live = append(live, e)
	}
	i := 0
	for b.Loop() {
		w.Destroy(live[i%len(live)])
		e := w.Create()
		ps.Set(e, pos{})
		vs.Set(e, vel{})
		live[i%len(live)] = e
		i++
	}
}
