package ecs

import "sort"

// Store holds one component type. dense is kept sorted by entity index so
// Each is deterministic and cache-friendly; sparse maps slot index -> dense position.
type Store[T any] struct {
	sparse []int32 // -1 = absent
	dense  []Entity
	data   []T
}

// NewStore creates a store and registers it so World.Destroy clears it.
func NewStore[T any](w *World) *Store[T] {
	s := &Store[T]{}
	w.stores = append(w.stores, s)
	return s
}

func (s *Store[T]) pos(e Entity) int {
	idx := int(e.index())
	if idx >= len(s.sparse) {
		return -1
	}
	p := int(s.sparse[idx])
	if p < 0 || s.dense[p] != e {
		return -1
	}
	return p
}

// Set adds or replaces e's component.
// shortcut: insertion into the middle is O(n) to keep sort order; fine for
// hundreds of entities, revisit if a store holds tens of thousands with churn.
func (s *Store[T]) Set(e Entity, v T) {
	if p := s.pos(e); p >= 0 {
		s.data[p] = v
		return
	}
	idx := int(e.index())
	for len(s.sparse) <= idx {
		s.sparse = append(s.sparse, -1)
	}
	// A stale generation may still occupy this slot; drop it first.
	if p := s.sparse[idx]; p >= 0 {
		s.removeAt(int(p))
	}
	p := sort.Search(len(s.dense), func(i int) bool { return s.dense[i].index() > e.index() })
	var zero T
	s.dense = append(s.dense, 0)
	s.data = append(s.data, zero)
	copy(s.dense[p+1:], s.dense[p:])
	copy(s.data[p+1:], s.data[p:])
	s.dense[p], s.data[p] = e, v
	for i := p; i < len(s.dense); i++ {
		s.sparse[s.dense[i].index()] = int32(i)
	}
}

// Get returns a pointer to e's component. The pointer is invalidated by Set/Remove on this store.
func (s *Store[T]) Get(e Entity) (*T, bool) {
	if p := s.pos(e); p >= 0 {
		return &s.data[p], true
	}
	return nil, false
}

// Has reports whether e has this component.
func (s *Store[T]) Has(e Entity) bool { return s.pos(e) >= 0 }

// Remove deletes e's component if present.
func (s *Store[T]) Remove(e Entity) {
	if p := s.pos(e); p >= 0 {
		s.removeAt(p)
	}
}

func (s *Store[T]) remove(e Entity) { s.Remove(e) }

func (s *Store[T]) removeAt(p int) {
	s.sparse[s.dense[p].index()] = -1
	copy(s.dense[p:], s.dense[p+1:])
	copy(s.data[p:], s.data[p+1:])
	s.dense = s.dense[:len(s.dense)-1]
	s.data = s.data[:len(s.data)-1]
	for i := p; i < len(s.dense); i++ {
		s.sparse[s.dense[i].index()] = int32(i)
	}
}

// Len is the number of entities with this component.
func (s *Store[T]) Len() int { return len(s.dense) }

// Each visits every component in ascending entity order. fn must not Set or
// Remove on this store; collect entities and mutate after iterating.
func (s *Store[T]) Each(fn func(Entity, *T)) {
	for i := range s.dense {
		fn(s.dense[i], &s.data[i])
	}
}
