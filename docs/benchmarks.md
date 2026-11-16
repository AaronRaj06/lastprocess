# Benchmarks

Every number below comes from a command in this repo. The value shown is the median of 5 runs (`-count 5`).

**Hardware:** Apple M1, 8 cores, macOS (Darwin 27), Go 1.27.4.

## ECS and sim (Phase 1)

Command:

```
go test -run '^$' -bench . -benchmem -count 5 ./internal/...
```

| Benchmark | What it measures | Median | Allocs |
|---|---|---|---|
| `BenchmarkMovementSystem/n=16` | `pos += vel` over 16 entities (a full lobby) | 77.0 ns/op, **208 M entities/s** | 0 |
| `BenchmarkMovementSystem/n=400` | the same over 400 entities (lobby plus projectiles) | 1.86 µs/op, **215 M entities/s** | 0 |
| `BenchmarkMovementSystem/n=100000` | synthetic stress test | 488 µs/op, **205 M entities/s** | 0 |
| `BenchmarkSpawnDespawn` | destroy 1 entity, create 1, attach 2 components, store size 400 | 997 ns/op | 0 |
| `world.BenchmarkStep16` | one full `Sim.Step` with 16 combatants and random actions | 588 ns/op | 1 (128 B) |

How to read these numbers:
- Throughput stays flat from 16 to 100k entities, so per-entity cost does not depend on store size. Each `Get` is one sparse-array lookup.
- Spawn and despawn cost about 1 µs because each store is kept sorted by entity ID, which makes Set and Remove O(n). This is the price of deterministic iteration order. It stays cheap at game sizes and is noted as a `shortcut:` in `internal/ecs/store.go`.
- A tick takes 0.6 µs, while the 20 Hz tick budget is 50 ms. The sim is nowhere near the bottleneck yet. Phase 7 will measure where the real limit is (rendering and SSH writes).

## Still to measure

These are filled in by later phases: concurrent matches and sessions, input-to-frame latency, diff vs full frame size, per-bot tick cost, RL win rate, determinism seeds in CI, sandbox attack cases, coverage.

Current coverage (`go test -cover ./internal/...`): ecs 97.3%, world 93.7%.
