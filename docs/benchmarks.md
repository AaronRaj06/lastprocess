# Benchmarks

Every number here was produced by a command in this repo on 2026-11-16. Rerun the command to reproduce it.

**Hardware:** Apple M1 (8 cores), macOS (Darwin 27), Go 1.27.4. Server and load generator ran on the same machine, so the load numbers are conservative for the server and say nothing about a real network.

## Summary table

| Metric | Value | Command |
|---|---|---|
| Max concurrent load at a stable 20 Hz | **1,600 SSH sessions in 201 matches**, 0 failed sessions, tick lateness p99 0.04 ms | `make loadtest` |
| Input-to-frame latency, 100 to 800 sessions | **p50 26 ms, p99 50 ms** | `make loadtest` |
| Input-to-frame latency, 1,600 sessions | p50 26 ms, p99 71 ms | `make loadtest` |
| Bytes per frame, full redraw vs diff | 5,631 B vs **84 B (98.5% less)** | `go test -run '^$' -bench FrameBytes ./internal/render` |
| Bytes per frame measured live over SSH | 57 to 63 B per frame, 1.2 kB/s per session | `make loadtest` |
| Tick cost per combatant: native baseline | **8.8 ns** | `go test -run '^$' -bench Act ./internal/bot` |
| Tick cost per combatant: Lua bot in the sandbox | **13.7 µs** (about 1,600x native) | `go test -run '^$' -bench LuaAct ./internal/sandbox` |
| Tick cost per combatant: RL policy (259-256-256-13 MLP) | **94 µs** | `go test -run '^$' -bench PolicyAct ./internal/bot` |
| ECS movement system | **209 to 218 M entities/s**, 0 allocations | `go test -run '^$' -bench Movement ./internal/ecs` |
| Sim step, 16 combatants | 508 ns per tick | `go test -run '^$' -bench Step16 ./internal/world` |
| Headless throughput, 8 baseline bots | 799 matches/s, 0.75 M ticks/s (4,690x real time per core) | `go run ./cmd/headless -matches 5000 -bots 'baseline*8'` |
| Baseline vs random, duel | baseline wins **80.3%** (n = 5,000) | `go run ./cmd/headless -matches 5000 -bots baseline,random` |
| Baseline vs 7 random bots | baseline wins **43.9%** (chance 12.5%, n = 5,000) | `go run ./cmd/headless -matches 5000 -bots 'baseline,random*7'` |
| RL bot vs baseline, duel | RL wins **56.0%**, baseline 42.4%, no survivor 1.6% (n = 2,000) | `make eval` |
| RL bot vs 7 baselines | RL wins 16.9% (chance 12.5%, n = 2,000) | `go run ./cmd/headless -matches 2000 -seed 200000 -bots 'rl,baseline*7'` |
| Determinism | **50 full matches** run twice with the hash compared after every tick, then replayed from the log bit-identically; plus 20 seeds x 1,000 ticks with shuffled input order. In CI under `-race`. | `go test -race -run 'Deterministic' ./internal/match ./internal/world` |
| Sandbox attack suite | **28 attack cases, all contained**; 27 blocked globals unreachable | `go test -v -run 'Attacks\|Blocked' ./internal/sandbox` |
| Go ONNX runner vs PyTorch | max logit difference 4.8e-06 over 16 vectors | `go test -v -run PolicyMatches ./internal/bot` |
| Test coverage | ecs 97.3%, world 91.2%, sandbox 95.9%, render 98.5%, replay 94.4%, session 90.7%, match 89.7%, store 83.1%, bot 72.1% | `make cover` |
| Docker image | 24.4 MB, static binary, non-root, read-only root filesystem | `make docker` |

## Load test detail

`cmd/loadtest` opens real SSH sessions (PTY, 80x43), and each one sends about five keys per second. Sixteen probe sessions run a terminal emulator and time each key from the moment it is sent until a frame arrives whose HUD shows the server applied it. The server reports how late each tick fired against the 50 ms schedule. "Stable" means tick lateness p99 under 5 ms and fewer than 1% of ticks more than 5 ms late. Each step measured 20 seconds after an 8 second warm-up. Eight humans per match, no bots.

| Sessions | Matches | Failed sessions | Tick late p99 / max | Ticks > 5 ms late | Tick work p99 | Input-to-frame p50 / p99 | Stable |
|---|---|---|---|---|---|---|---|
| 100 | 13 | 0 | 0.04 / 4.8 ms | 0 of 5,200 | 0.08 ms | 25.6 / 50.0 ms | yes |
| 400 | 51 | 0 | 0.03 / 0.1 ms | 0 of 20,402 | 0.05 ms | 25.7 / 50.0 ms | yes |
| 800 | 101 | 0 | 0.04 / 25.4 ms | 2 of 40,408 | 0.05 ms | 25.8 / 50.1 ms | yes |
| 1,600 | 201 | 0 | 0.04 / 5.7 ms | 67 of 80,402 | 0.17 ms | 25.9 / 70.5 ms | yes |
| 2,400 | 298 | 25 | 0.15 / 51.7 ms | 267 of 119,008 | 0.35 ms | 27.9 / 170.0 ms | degraded |
| 3,200 | 382 | 159 | 37.8 / 200 ms | 1,715 of 154,815 | 0.66 ms | 39.0 / 344.5 ms | no |

How to read it:
- A key waits for the next tick, so latency is spread evenly over 0 to 50 ms. A p50 of 26 ms and a p99 of 50 ms mean the server adds almost nothing on top of the tick.
- The sim is not the limit: stepping a match and fanning out its snapshot takes under 0.2 ms at p99 even with 201 matches. What gives out first is SSH encryption and scheduling for thousands of connections, shared with a load generator that is doing the same work on the same 8 cores.
- Percentiles come from the most recent 32,768 tick samples.
- These numbers are from one run; they were not repeated to get error bars.

## Frame size

`BenchmarkFrameBytes` plays whole matches with 8 scripted combatants and renders each tick for one player on an 80x43 terminal. Full redraw: 5,631 bytes. Diff: 84 bytes on average, including the first full frame of each match. `TestDiffStreamReproducesFrames` feeds the diff stream into a terminal emulator and checks that every frame matches, for three terminal sizes including one small enough that the camera scrolls.

## Bot cost

| Bot | Per decision | Notes |
|---|---|---|
| Native baseline | 8.8 ns, 0 allocations | plain Go |
| Lua (chaser example) | 13.7 µs, 52 allocations | most of it is building the observation table (about 240 entries) and entering the limit context; the script itself uses about 50 VM ticks of its 20,000 budget |
| RL policy | 94 µs, 3 allocations | about 133,000 scalar float32 multiply-adds, no SIMD |

A match with 4 RL bots and 4 baseline bots therefore spends about 0.4 ms of each 50 ms tick on bots.

## RL result

Training: PPO (Stable-Baselines3), 64 parallel duels against the baseline, MLP 256-256, config and curves in `trainer/runs/`. Evaluation: the exported ONNX policy, run by the Go server's own inference code, acting greedily, on 2,000 seeds that were never used in training.

| Run | Change | Steps | Duel win rate vs baseline (n = 2,000) |
|---|---|---|---|
| ppo-v1 | raw observation vector | 8 M | 44.5% |
| ppo-v2 | longer, learning rate decay | 30 M | 47.4% |
| ppo-v3 | ray-cast features (D15) | 16 M | 47.9% |
| ppo-v4 | incoming-projectile field | 16 M | 48.5% (baseline 50.5%) |
| ppo-v5 (shipped) | imitation warm start on the baseline (3 DAgger rounds of 300k labelled states), 0.5 M steps with the actor frozen, then PPO at a lower learning rate | 8.4 M | **56.0%** (baseline 42.4%) |

The acceptance criterion "beats the scripted baseline" is met by ppo-v5 and by none of the four from-scratch runs. With n = 2,000 the standard error is about 1.1 points, so a 13.6-point gap is well outside noise. Checks on the shipped policy:

| Check | Result | Command |
|---|---|---|
| Second seed set | RL 55.8%, baseline 42.3% (n = 2,000) | `go run ./cmd/headless -matches 2000 -seed 500000 -bots rl,baseline` |
| RL in the second slot | RL 53.8%, baseline 44.5% (n = 2,000) | `go run ./cmd/headless -matches 2000 -seed 100000 -bots baseline,rl` |
| vs random | RL 80.3%, random 19.3% (n = 1,000) | `go run ./cmd/headless -matches 1000 -seed 100000 -bots rl,random` |

The cloned policy alone (before any PPO) scored 43.5% against the baseline's 44.9%, with only about 61% action agreement, so the gain over parity comes from the fine-tuning. The training-time win rate peaked near 58% at 4 M steps and ended at 54%; only the final checkpoint was exported and evaluated. This is a single training seed.

Our reading (a hypothesis, not tested): from scratch, PPO converges to a noisy mirror of the baseline's "line up, then shoot every 5 ticks" and stays there; starting from a clone of that rule lets it spend its training on the small improvements instead. We have not analysed what the policy does differently. A mirror duel between two baselines has no slot-order bias (44.3% vs 43.4% over 20,000 matches, `-per-slot`), so the environment itself is fair.

What would be tried next: self-play or a league instead of a fixed opponent, training in 8-player matches (the policy has only ever seen duels), and a recurrent policy.

## Notes on the Phase 1 numbers

Throughput of the ECS movement benchmark is flat from 16 to 100,000 entities. Spawn plus despawn costs about 1 µs at 400 entities because each store stays sorted by entity ID; that is the price of deterministic iteration order.
