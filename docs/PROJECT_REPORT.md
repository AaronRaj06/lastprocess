# Last Process Standing: project report

What was built, mapped against the original project spec. Written 2026-11-19 at commit `904e122` (tag `v0.1.0` is live). Every number here comes from `docs/benchmarks.md`, which records the command that produced it.

```
ssh lastprocess-arena.duckdns.org
```

## 1. Summary

| | |
|---|---|
| What it is | A real-time multiplayer arena game played over SSH, with no install. Humans, RL bots and sandboxed Lua bots fight in the same match through one action interface. |
| Status | All 8 phases done. Public server live on Azure since 2026-11-18. |
| Code | About 5,550 lines of Go (including tests) and 335 lines of Python (trainer). 19 commits, 2026-11-16 to 2026-11-18. |
| CI | GitHub Actions: `go vet`, `go test -race`, `golangci-lint`, `pytest`. Tagging `v*` builds an amd64 + arm64 image, pushes it to GHCR and restarts the server. |
| Open items | Not yet tested from a second, independent network. Azure credit to be logged weekly (section 7). |

## 2. Definition of done (brief section 12)

| Requirement | State | Evidence |
|---|---|---|
| Public `ssh play.<domain>` works and always has a match running | **Done, with a different address** | `ssh lastprocess-arena.duckdns.org` (free DuckDNS name, not a bought domain). One bot-only match always runs (D14). Tested from a home connection over the internet. |
| Humans, RL bots and Lua bots fight in one match through one interface | Done | `TestLiveMatchHumansBotsAndLua` (`internal/match`). The live server fills empty slots with RL and baseline bots. |
| Replays reproduce bit-identically, verified in CI | Done, plus more | 50 matches hashed every tick and replayed. A replay recorded on arm64 also reproduces on amd64 in CI, bots included (`TestGoldenReplayAcrossArch`). |
| Adversarial sandbox suite passes | Done | 28 attack cases contained, 27 blocked globals unreachable. Passes on the M1, in CI and on the VM. |
| Benchmark table complete and reproducible | Done | `docs/benchmarks.md`: every row has value, hardware and command, measured on the M1 and on the production VM. |
| README, architecture doc and demo GIF | Done | `README.md`, `docs/architecture.md`, `docs/demo.gif` (recorded from `docs/demo.tape`). |

## 3. Phase by phase (brief section 7)

### Phase 1: Engine foundation. Done.
| Planned | Delivered |
|---|---|
| Scaffold, Makefile, lint, CI (`vet`, `test -race`, `golangci-lint`) | Done. CI lives in `.github/workflows` because GitHub only reads it there (D6). |
| Own ECS with tests and a benchmark | `internal/ecs`: sorted sparse sets so iteration order never depends on history (D2). 97.3% coverage. Movement system 209 to 218 M entities/s, 0 allocations. |
| Seeded map, tick, movement, collision | `internal/world`: BSP rooms and corridors, one PCG RNG per match whose state is part of the hash (D7). Contested moves resolve by entity ID (D3). |
| Local single-player TUI | `cmd/local` (Bubble Tea). Extra entry point, recorded as D5. |
| Same seed gives same map | Tested. |

The wall-clock tick loop sits outside `internal/world` so the sim never reads the clock (D4).

### Phase 2: SSH multiplayer. Done.
| Planned | Delivered |
|---|---|
| Wish SSH server, shared match, per-player input queue, 20 Hz | `internal/session`, `internal/match`. |
| Diff renderer with bytes per frame | `internal/render`: 5,631 B full redraw against 84 B diff (98.5% less). Live over SSH: 57 to 63 B per frame, about 1.2 kB/s per player. A test feeds the diff stream into a terminal emulator and checks every frame. |
| Spectate | `spectate` command. |
| Two real SSH clients in one match | `TestTwoClientsPlayOneMatch`. |

**Deviation (D8):** the SSH sessions write raw ANSI diffs instead of using Bubble Tea. Bubble Tea owns the output stream and redraws whole lines, so cell-level diffs could not be controlled or measured through it. Bubble Tea is kept for the local client.

### Phase 3: Combat, zone, replays. Done.
| Planned | Delivered |
|---|---|
| Shooting, projectiles, dash, pickups, shrinking zone, win and tick-cap tiebreak | Done in `internal/world` (91.2% coverage). |
| Replay log and `replay <id>` | `internal/replay`: seed plus one action byte per combatant per tick, gzipped, a few KB per match. Tamper test included. |
| Determinism test in CI | 50 full matches run twice with the hash compared after every tick, then replayed. Plus 20 seeds with shuffled input order. Runs under `-race`. |

**Design choice (D9):** replays store every combatant's actions and never re-run the bots, so a replay depends only on the sim. The bots are still deterministic, and the cross-architecture golden test checks that separately.

### Phase 4: Headless sim and baseline. Done.
| Planned | Delivered |
|---|---|
| `cmd/headless`, much faster than real time | 799 matches/s, 0.75 M ticks/s on the M1 (4,690x real time per core). 330 matches/s on the 2-vCPU VM. |
| Native scripted baseline | `internal/bot`: avoids the zone, chases the nearest enemy, shoots when aligned. 8.8 ns per decision. |
| One bot interface for every combatant kind | Done. The sim cannot tell humans, baseline, RL and Lua apart. |
| Baseline vs random win rate | 80.3% in duels (n = 5,000). 43.9% against 7 random bots (chance is 12.5%). |

### Phase 5: RL pipeline. Done.
| Planned | Delivered |
|---|---|
| Gym env over IPC, decision documented | `trainer/lps_env.py` over one TCP socket with binary frames carrying all environments per message (D11). About 25,000 env steps/s with 64 environments. Both `gymnasium.Env` and an SB3 `VecEnv`. |
| PPO with reproducible config and logged curves | `trainer/train.py`, configs and curves in `trainer/runs/`. |
| ONNX export and server-side inference | Exported to ONNX, run by a small ONNX reader written in Go, with no ONNX Runtime and no cgo (D12). Matches PyTorch to 4.8e-6. No FMA, so results are identical on arm64 and amd64. |
| Beats the baseline over at least 500 matches | **ppo-v5 wins 56.0% against the baseline's 42.4% (n = 2,000 held-out seeds)**. Standard error about 1.1 points. Confirmed on a second seed set (55.8%) and with the slots swapped (53.8%). |
| Bots fill empty slots; policy runs live | `-fill rl,baseline` on the live server. |

How it got there: four from-scratch PPO runs reached only parity (44.5% to 48.5%), even after adding ray-cast features (D15). An imitation warm start on the baseline (DAgger) followed by PPO fine-tuning beat it. Honest limits: one training seed, final checkpoint only, trained on duels. In 8-player matches its edge is small (16.9% against 12.5% chance).

### Phase 6: Lua sandbox. Done.
| Planned | Delivered |
|---|---|
| Section 6 spike, decision recorded | D10 compares the four options with measured evidence. gopher-lua can count instructions, but one instruction can do unbounded work: a 268 MB string, and a `string.find` that ran over 2 minutes. cgo Lua was rejected because it breaks the static binary and puts C next to hostile input. WASM was too much work. **Chosen: `arnodel/golua`**, where library functions also pay CPU in proportion to their work and allocations are charged before they happen. |
| `upload-bot` over SSH, size-checked, stored per account | Done. Accounts are SSH key fingerprints, and guests need no key (D13). Tested: valid upload, infinite-loop upload, bad name, oversize. |
| CPU, memory and size limits | Deterministic: 20,000 VM ticks per `act` call, 256 KB allocation per call, 16 MB per match, 16 KB script size. 10 violations disqualify the bot. Seeded `rand()`. |
| Example bots | `bots/examples/`: chaser, turret, wanderer. |
| Upload a bot that fights live under limits | Done (`play <bot>`). |

### Phase 7: Hardening, load tests, benchmarks. Done.
| Planned | Delivered |
|---|---|
| Adversarial suite: infinite loops, memory bombs, recursion, string bombs, blocked globals, other bots' state, huge returns | 28 cases, all contained. Under `-race` the test now bounds the slowest single call instead of the total time. The total had gone red in CI only for timing reasons. |
| Load test, stable limit at 20 Hz | `cmd/loadtest` opens real SSH sessions with a PTY and times each key until the frame shows it applied. **M1: 1,600 sessions in 201 matches, stable. Production VM: 800 sessions in 101 matches, stable; 1,200 not stable.** |
| All numbers in `docs/benchmarks.md` | Done (section 4 below). |

### Phase 8: Deploy and polish. Done.
| Planned | Delivered |
|---|---|
| Dockerfile, host config, deploy on tag | 24.4 MB distroless static image, non-root, read-only root filesystem, 512 MB memory cap. Built for amd64 and arm64 in CI by cross-compiling, with no emulation. Deployed over SSH by a separate deploy key. |
| Persistent SQLite, host key persistence, rate and connection limits | Docker volume for `/data`. Host key unchanged across a VM stop and start. Limits: 256 sessions, 4 per IP, 1 new connection per second. |
| Leaderboard | Win counts (`leaderboard`). |
| README, architecture diagram, demo GIF | Done. The README has the `ssh` line first, the diagram, the benchmark table and the design-decisions section the brief asked for. |
| `ssh play.<domain>` from a fresh machine | Works at `lastprocess-arena.duckdns.org`. Deploy run 44345538888. |

## 4. Benchmark table (brief section 8)

| Metric | Apple M1 (8 cores) | Azure B2pts_v2 (2 Arm vCPU, 1 GiB) |
|---|---|---|
| Max load at a stable 20 Hz | 1,600 sessions / 201 matches | 800 sessions / 101 matches |
| Input-to-frame latency p50 / p99 | 26 / 50 ms | 25 to 27 / 51 ms (100 to 400 sessions) |
| Bytes per frame, full vs diff | 5,631 vs 84 B (98.5% less) | 5,649 vs 85 B |
| Tick cost: native / Lua / RL | 8.8 ns / 13.7 µs / 94 µs | 13.0 ns / 25.5 µs / 125 µs |
| ECS movement | 209 to 218 M entities/s | 153 to 165 M entities/s |
| RL vs baseline (n = 2,000) | 56.0% vs 42.4% | identical result on the same seeds |
| Determinism in CI | 50 matches + 20 shuffled-input seeds + 1 cross-architecture replay | |
| Sandbox attack cases contained | 28 of 28 | 28 of 28 |
| Coverage | ecs 97.3%, world (the sim) 91.2%, sandbox 95.9% | |
| Server memory | | 236 MB RSS at 1,200 sessions |

Load tests ran the load generator on the same machine as the server, so they share its CPU. Each was one run, with no error bars. Over the internet, latency grew with session count, but the VM stayed about 84% idle at 200 sessions. The growth came from the client side.

## 5. Where the result differs from the plan

| Brief said | What happened | Why | Recorded in |
|---|---|---|---|
| Host on Fly.io or Oracle free tier | Azure for Students | Fly.io has no free allowance, and port 22 needs a paid IP. Oracle sign-up failed. Azure for Students needs no payment method and cannot bill one. | D1, D16 |
| `B1s` VM size (from the hosting brief) | `Standard_B2pts_v2` (Arm, 1 GiB) | The subscription's policy allows five regions, and B1s is unavailable in all of them. | `docs/azure-budget.md` |
| `ssh play.<domain>` | `ssh lastprocess-arena.duckdns.org` | No paid domain (zero-cost rule) | README |
| Strict zero cost | About $6.50 a month from the $100 student credit, | The only option with no billing risk. If the credit runs out, the server stops, and nothing is billed. | `docs/azure-budget.md` |
| Bubble Tea for SSH sessions | Raw ANSI diff renderer; Bubble Tea only in `cmd/local` | Cell-level diffs could not be controlled or measured through Bubble Tea | D8 |
| Tick loop in `internal/world` | Loop in the callers; the sim only exposes `Step` | Keeps the sim free of the clock | D4 |
| `fly.toml`, CI in `/deploy` | `deploy/` holds the Dockerfile and compose file; CI is in `.github/workflows` | GitHub only reads workflows there | D6 |
| ONNX runtime or a sidecar | A small ONNX reader written in Go | No cgo, no 30 MB library, no second process on a 1 GiB VM | D12 |
| Ratings "if time allows" | Win counts only | Cut-order item 2 | |
| WASM or multi-language bots | Not built | Cut-order item 1 | D10 |

Nothing on the brief's "never cut" list (determinism tests, benchmarks, public deploy) was cut.

## 6. Known weaknesses (stated, not hidden)

- **Sandbox:** golua's limits are marked alpha upstream. We pin v0.4.1 and rely on our own suite. Memory is counted as bytes allocated, not bytes live, so it is a budget, not a heap limit. A VM tick bounds real time only up to a constant factor. A Go stack overflow or a runtime out-of-memory inside golua cannot be caught, so the container cap is the last line of defence.
- **RL:** one training seed, final checkpoint only, duel-trained, with no analysis of what the policy does differently. The 8-player edge is small.
- **Sim:** contested moves favour the lower entity ID (D3).
- **Hosting:** a temporary demo host tied to a student account, with 1 GiB RAM and burstable CPU. The static IP and disk cost credit even while the VM is stopped. If it goes away, the plan is in `docs/FALLBACK.md` (local demo with `make demo` or the public image).
- **Benchmarks:** single runs, with the load generator sharing the server's machine.

## 7. Operations

| Task | How |
|---|---|
| Deploy | Push a `v*` tag. CI builds, pushes to `ghcr.io/aaronraj06/lastprocess` and restarts the container. |
| Cost check (read-only) | `scripts/azure-cost-check.sh`. The credit balance is visible only in the portal; log it weekly in `docs/azure-budget.md`. |
| Stop or start the VM | `scripts/azure-stop.sh` (backs up first) and `scripts/azure-start.sh`. |
| Backup | `scripts/backup.sh`: briefly stops the game so SQLite is consistent, and never leaves a partial file. |
| Budget triggers | Over 30% used in the first month, or under $20 left: switch to demo-window mode. Details in `docs/azure-budget.md`. |

Still to do: one test from a second network (for example a phone hotspot), and the weekly credit log, including a check that compute is not being charged (about $6.50 a month expected, about $15 if it is).

## 8. Where to look

| Topic | File |
|---|---|
| Architecture | `docs/architecture.md` |
| Bot observation and action API | `docs/bot-api.md` |
| Every decision with context and consequences | `docs/decisions.md` (D1 to D16) |
| All numbers with commands | `docs/benchmarks.md` |
| Audit and status log | `docs/STATUS.md` |
| Hosting cost and rules | `docs/azure-budget.md`, `docs/FALLBACK.md` |
| Server setup | `deploy/README.md` |
