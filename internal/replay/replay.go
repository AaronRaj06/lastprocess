// Package replay records a match as (seed, ordered actions per tick) and
// re-simulates it. Bots are not re-run on replay: their recorded actions are
// fed back, so a replay depends only on the sim being deterministic.
package replay

import (
	"bytes"
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"io"

	"github.com/AaronRaj06/lastprocess/internal/ecs"
	"github.com/AaronRaj06/lastprocess/internal/world"
)

// maxDecoded bounds a decoded log so a corrupt blob cannot exhaust memory.
const maxDecoded = 4 << 20

// Log is everything needed to reproduce a match.
type Log struct {
	Seed    uint64
	Names   []string // one per combatant, in spawn order
	Actions []byte   // len(Names) actions per tick, tick-major
	Hash    uint64   // world.Sim.Hash after the last tick
}

// Record appends one tick of actions (one per combatant, in spawn order).
func (l *Log) Record(actions []world.Action) {
	for _, a := range actions {
		l.Actions = append(l.Actions, byte(a))
	}
}

// Ticks is the number of recorded ticks.
func (l *Log) Ticks() int { return len(l.Actions) / max(1, len(l.Names)) }

// Encode serialises the log (gob, gzipped).
func (l *Log) Encode() []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := gob.NewEncoder(zw).Encode(l); err != nil {
		panic(err) // a struct of plain slices always encodes
	}
	_ = zw.Close()
	return buf.Bytes()
}

// Decode parses an encoded log.
func Decode(b []byte) (*Log, error) {
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	var l Log
	if err := gob.NewDecoder(io.LimitReader(zr, maxDecoded)).Decode(&l); err != nil {
		return nil, err
	}
	if len(l.Names) == 0 || len(l.Actions)%len(l.Names) != 0 {
		return nil, fmt.Errorf("replay: malformed log")
	}
	return &l, nil
}

// Player re-simulates a log one tick at a time.
type Player struct {
	Sim  *world.Sim
	IDs  []ecs.Entity
	log  *Log
	tick int
	in   []world.Input
}

// NewPlayer creates the match exactly as the original was created.
func NewPlayer(l *Log) *Player {
	p := &Player{Sim: world.New(world.Config{Seed: l.Seed}), log: l}
	for range l.Names {
		p.IDs = append(p.IDs, p.Sim.Spawn())
	}
	p.in = make([]world.Input, len(p.IDs))
	return p
}

// Step applies the next recorded tick. It returns false when the log is exhausted.
func (p *Player) Step() bool {
	if p.tick >= p.log.Ticks() {
		return false
	}
	n := len(p.IDs)
	for i, a := range p.log.Actions[p.tick*n : (p.tick+1)*n] {
		p.in[i] = world.Input{Combatant: p.IDs[i], Action: world.Action(a)}
	}
	p.Sim.Step(p.in)
	p.tick++
	return true
}

// Verify re-simulates the whole log and checks the final state hash.
func (l *Log) Verify() error {
	p := NewPlayer(l)
	for p.Step() {
	}
	if h := p.Sim.Hash(); h != l.Hash {
		return fmt.Errorf("replay: hash mismatch: got %016x, recorded %016x", h, l.Hash)
	}
	return nil
}
