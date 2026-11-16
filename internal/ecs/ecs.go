// Package ecs is a minimal entity-component store built for determinism:
// every iteration runs in ascending entity order, never Go map order.
package ecs

// Entity packs a 24-bit slot index and an 8-bit generation. Zero is Nil.
type Entity uint32

const (
	indexBits = 24
	indexMask = 1<<indexBits - 1
	// MaxEntities is the number of live entities a World can hold.
	MaxEntities = indexMask
)

// Nil is the zero entity; it is never returned by Create.
const Nil Entity = 0

func (e Entity) index() uint32 { return uint32(e) & indexMask }
func (e Entity) gen() uint8    { return uint8(uint32(e) >> indexBits) }

func makeEntity(idx uint32, gen uint8) Entity { return Entity(uint32(gen)<<indexBits | idx) }

type remover interface{ remove(Entity) }

// World allocates entities and removes their components on Destroy.
type World struct {
	gens   []uint8  // generation per slot; slot 0 unused so Nil stays invalid
	live   []bool   // live per slot
	free   []uint32 // LIFO free list of slot indices
	stores []remover
	alive  int
}

// NewWorld returns an empty World.
func NewWorld() *World { return &World{gens: make([]uint8, 1), live: make([]bool, 1)} }

// Create returns a new live entity, reusing freed slots LIFO.
func (w *World) Create() Entity {
	w.alive++
	if n := len(w.free); n > 0 {
		idx := w.free[n-1]
		w.free = w.free[:n-1]
		w.live[idx] = true
		return makeEntity(idx, w.gens[idx])
	}
	idx := uint32(len(w.gens))
	if idx > MaxEntities {
		panic("ecs: entity limit reached")
	}
	w.gens = append(w.gens, 0)
	w.live = append(w.live, true)
	return makeEntity(idx, 0)
}

// Alive reports whether e was created and not yet destroyed.
func (w *World) Alive(e Entity) bool {
	idx := e.index()
	return idx != 0 && int(idx) < len(w.gens) && w.live[idx] && w.gens[idx] == e.gen()
}

// Destroy removes e from every store and frees its slot. Destroying a dead entity is a no-op.
func (w *World) Destroy(e Entity) {
	if !w.Alive(e) {
		return
	}
	for _, s := range w.stores {
		s.remove(e)
	}
	idx := e.index()
	w.gens[idx]++ // 8-bit generation wraps after 256 reuses of one slot; stale handles that old are not detected
	w.live[idx] = false
	w.free = append(w.free, idx)
	w.alive--
}

// Len is the number of live entities.
func (w *World) Len() int { return w.alive }
