```
ssh play.<your-domain>
```

# Last Process Standing

A multiplayer, real-time battle royale that runs in your terminal over SSH. No install, no sign-up: 8 processes spawn on a generated ASCII map, the safe zone shrinks, the last one alive wins. Empty slots are filled by bots, so a match is always running.

> **Status:** everything below runs and is tested locally and in Docker. The public server is not deployed yet (it needs a cloud account and a domain; steps are in [`deploy/README.md`](deploy/README.md)). Until then: `make run`, then `ssh -p 2222 localhost`.

![demo](docs/demo.gif)

Three kinds of players fight in the same match through one interface, and the simulation cannot tell them apart:

1. **People** over SSH.
2. **An RL bot** trained with PPO on a headless copy of the sim, exported to ONNX and run inside the server.
3. **Lua bots** written by players, uploaded over SSH and run in a sandbox with a deterministic CPU budget.

## Try it

```
make run                                  # server on :2222
ssh -p 2222 localhost                     # play: w a s d move, arrows shoot, W A S D dash
ssh -p 2222 -t localhost spectate         # watch
ssh -p 2222 localhost upload-bot mybot < bots/examples/chaser.lua
ssh -p 2222 -t localhost play mybot       # play alongside your bot
ssh -p 2222 -t localhost replay           # list matches; "replay 12" re-simulates one
ssh -p 2222 localhost leaderboard
```

Other entry points: `make run-local` (single player, no server), `make headless` (thousands of bot matches), `make train` (PPO), `make loadtest`.

Writing a bot: [`docs/bot-api.md`](docs/bot-api.md).

## Architecture

```
   ssh clients ──> Wish SSH server ──> command router ──> match manager (20 Hz)
                                                               │
          human keys ─┐                                        ▼
          RL policy  ─┼── bot.Controller ──> match.Game ──> world.Sim (pure, deterministic)
          Lua sandbox ┘                          │               │
                                                 │               └─> replay log ──> SQLite
   cmd/headless (same Game, no clock) ───────────┘
        └─ TCP env server ──> Python PPO trainer ──> policy.onnx (embedded in the server)

   world.View snapshot ──> per-session frame buffer ──> cell diff ──> SSH channel
```

More in [`docs/architecture.md`](docs/architecture.md).

## Measured

Apple M1, server and load generator on the same machine. Full table with commands: [`docs/benchmarks.md`](docs/benchmarks.md).

| | |
|---|---|
| Concurrent load at a stable 20 Hz | 1,600 SSH sessions in 201 matches |
| Input-to-frame latency (up to 800 sessions) | p50 26 ms, p99 50 ms |
| Bytes per frame | 84 with the diff renderer vs 5,631 for a full redraw (98.5% less) |
| Bot cost per decision | native 8.8 ns, Lua sandbox 13.7 µs, RL policy 94 µs |
| ECS movement system | about 210 M entities/s, 0 allocations |
| Headless sim | 0.75 M ticks/s on 8 cores (4,690x real time per core) |
| Determinism | 50 full matches hashed every tick and replayed bit-identically, in CI |
| Sandbox | 28 attack scripts contained, 27 dangerous globals unreachable |
| Scripted baseline vs random bot | wins 80.3% of 5,000 duels |
| RL bot vs scripted baseline | 48.5% of 2,000 duels: parity, **not a win** |
| Test coverage | ecs 97%, world 91%, sandbox 96%, render 98%, replay 94% |

## Design decisions

The full record, with the options that were rejected, is in [`docs/decisions.md`](docs/decisions.md).

**Server-authoritative.** Clients send one intent per tick (a key). The server owns all state and sends back pixels, so there is nothing on the client to tamper with, and a terminal is a sufficient client.

**Fixed 20 Hz tick.** The sim advances in whole ticks and never reads the clock; only the match loop does. That is what lets the same code run live at 20 Hz, headless at thousands of times real time for training, and step by step in tests. A slow SSH client drops frames instead of slowing the match.

**Deterministic.** Same seed plus same inputs gives byte-identical state: one seeded PCG per match, ECS stores that always iterate in entity order, inputs sorted before use, no map iteration, no goroutines in the step. This makes replays a few kilobytes (seed plus one byte per combatant per tick), makes every bug reproducible from a log, and makes RL training repeatable.

**One bot interface.** `Act(observation) -> action` is the only way to affect a match. People, the policy, Lua scripts and Go bots all implement it, so bots fill empty seats, bots train against the real game, and a new kind of player is one small adapter.

**The sandbox.** Uploaded Lua is treated as hostile. Each bot gets its own VM (golua, pure Go) with a whitelisted environment: no `os`, `io`, `load`, `require`, metatables or unseeded randomness. Each `act` call runs under a CPU budget counted in VM ticks, not wall time, so a bot is cut off at the same point on every machine, and under an allocation budget. An error or a blown budget is a no-op for that tick; ten of them disqualify the bot. A spike showed why the more popular gopher-lua was not enough: instruction counting works there, but a single instruction built a 268 MB string and another ran for over two minutes.

Its limits and known weaknesses:
- CPU is bounded in VM ticks, which bounds real time only up to a constant factor.
- Memory is counted as bytes allocated, not bytes live. It is a budget, not a heap limit.
- golua's quota support is marked alpha upstream. We pin the version and depend on our own attack suite.
- Bots run in the server process. A bug in the VM that overflows the Go stack or exhausts memory cannot be caught in-process; the container memory limit is the last defence. There is no per-bot OS isolation.
- A disqualified bot stands still until the zone kills it; it is not removed from the match.

**What did not work.** The RL bot does not beat the scripted baseline. Four training runs ended between 44.5% and 48.5% of evaluation duels. The pipeline (Gym environment, PPO, ONNX export, a Go inference path checked against PyTorch) works end to end; the policy is at parity, and the benchmarks file says what would be tried next.

## Repository layout

```
cmd/server      SSH game server            internal/world    the sim
cmd/headless    fast matches, RL env       internal/ecs      entity-component store
cmd/local       single-player client       internal/bot      controller interface, baseline, ONNX runner
cmd/loadtest    SSH load generator         internal/sandbox  Lua VM and limits
trainer/        Gym env, PPO, export       internal/match    game loop, manager, bot slotting
bots/examples   starter Lua bots           internal/render   frame buffer and diff encoder
deploy/         Dockerfile, compose        internal/replay   log and verifier
docs/           architecture, API,         internal/session  SSH sessions and commands
                decisions, benchmarks      internal/store    SQLite
```

## Development

```
make test      # unit and end-to-end tests (real SSH clients against a real server)
make race      # the same under the race detector
make lint      # go vet + golangci-lint
make bench     # benchmarks
make pytest    # trainer tests (needs uv)
make docker    # 24 MB static image
```
