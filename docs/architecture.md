# Architecture

Status: Phase 1 (engine foundation). This document grows phase by phase.

```
cmd/local ──(20 Hz tea.Tick)──> world.Sim.Step([]Input) ──> world.Snapshot() ──> TUI view
                                     │
                                     └── internal/ecs (World, Store[T])
```

## Packages

| Package | Role | May import |
|---|---|---|
| `internal/ecs` | Entities (index plus generation), generic sorted sparse-set component stores | stdlib only |
| `internal/world` | Pure sim: seeded map generation, inputs, movement, collision, state hash, snapshots | `ecs`, stdlib (no `time`, I/O, net, global RNG) |
| `cmd/local` | Single-player terminal client; owns wall-clock pacing | anything |

## Determinism contract

`world.New(Config{Seed})` followed by the same sequence of `Step(inputs)` calls gives the same `Hash()` after every tick. To guarantee this:
- All randomness comes from one per-match PCG, and its state is part of the hash.
- Iteration is always in ascending entity ID order (sorted ECS stores). The sim never ranges over a Go map.
- Inputs are sorted by entity before they are applied, and the last input per combatant wins. The order of the input slice does not matter.
- The sim never reads the clock. The caller decides when to call `Step`.

`TestStepDeterministic` checks this for 20 seeds × 1000 ticks × 8 combatants, feeding shuffled input order to the second sim.

## Map generation

Binary space partitioning over the interior: split until leaves are smaller than 2×(12×8), carve one room per leaf, and connect the two halves of every split with an L-shaped corridor. Every split is connected, so the whole map is connected. `TestMapConnectedWithWallBorder` checks this with a flood fill over 100 seeds.
