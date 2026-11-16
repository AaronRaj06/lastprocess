package ecs

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func TestCreateDestroyReuse(t *testing.T) {
	w := NewWorld()
	a := w.Create()
	if a == Nil || !w.Alive(a) {
		t.Fatalf("new entity should be alive and non-nil: %v", a)
	}
	w.Destroy(a)
	if w.Alive(a) {
		t.Fatal("destroyed entity still alive")
	}
	b := w.Create()
	if b.index() != a.index() || b.gen() == a.gen() {
		t.Fatalf("expected slot reuse with new generation: a=%x b=%x", a, b)
	}
	if w.Alive(a) || !w.Alive(b) {
		t.Fatal("stale handle must not alias the reused slot")
	}
	w.Destroy(a) // stale destroy is a no-op
	if !w.Alive(b) || w.Len() != 1 {
		t.Fatal("stale Destroy affected the live entity")
	}
	if w.Alive(Nil) {
		t.Fatal("Nil must never be alive")
	}
}

func TestStoreBasics(t *testing.T) {
	w := NewWorld()
	s := NewStore[int](w)
	a, b := w.Create(), w.Create()
	s.Set(a, 1)
	s.Set(b, 2)
	s.Set(a, 10)
	if v, ok := s.Get(a); !ok || *v != 10 {
		t.Fatalf("Get(a) = %v %v", v, ok)
	}
	s.Remove(a)
	if s.Has(a) || s.Len() != 1 {
		t.Fatal("Remove failed")
	}
	w.Destroy(b)
	if s.Len() != 0 {
		t.Fatal("Destroy must clear components")
	}
	// Stale handle must not read a reused slot's component.
	c := w.Create()
	s.Set(c, 3)
	if s.Has(b) {
		t.Fatal("stale handle sees new entity's component")
	}
}

// Iteration order must be ascending entity index regardless of insert/remove history.
func TestEachOrderIsDeterministic(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	w := NewWorld()
	s := NewStore[int](w)
	var live []Entity
	for range 2000 {
		if len(live) > 0 && r.IntN(3) == 0 {
			i := r.IntN(len(live))
			w.Destroy(live[i])
			live = slices.Delete(live, i, i+1)
			continue
		}
		e := w.Create()
		live = append(live, e)
		if r.IntN(2) == 0 {
			s.Set(e, int(e))
		}
	}
	var got []uint32
	s.Each(func(e Entity, v *int) {
		if Entity(*v) != e {
			t.Fatalf("component mismatch for %x", e)
		}
		got = append(got, e.index())
	})
	if !slices.IsSorted(got) {
		t.Fatal("Each not in ascending entity order")
	}
	for _, e := range live {
		if v, ok := s.Get(e); ok && Entity(*v) != e {
			t.Fatalf("Get mismatch for %x", e)
		}
	}
}
