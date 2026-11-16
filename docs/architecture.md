# Architecture

```
        ssh clients (players, spectators, bot uploaders)
                          |
                 Wish SSH server            internal/session
        auth (key = account) + rate limits
                          |
                   command router
   play | spectate | upload-bot | bots | replay | leaderboard | stats
                          |
                    match.Manager           internal/match
       one goroutine per live match, 20 Hz ticker
                          |
                     match.Game  <----------------  cmd/headless (no clock, no SSH)
        asks every bot.Controller for one action        |
              |              |            |             +-- -serve: RL env over TCP --> trainer/ (PPO)
           human           RL policy    Lua bot                                            |
        (last key)      (ONNX in Go)  (sandbox)   native baseline              policy.onnx (embedded)
              \              |            /
                     world.Sim.Step                internal/world on internal/ecs
                          |
              world.View snapshot  ----------->  replay.Log (seed + actions) --> SQLite
                          |
            render.Draw -> render.Diff          one frame buffer per session
                          |
                   bytes to the SSH channel
```

## Packages

| Package | Role | May import |
|---|---|---|
| `internal/ecs` | Entities (index plus generation) and generic sorted sparse-set component stores | stdlib |
| `internal/world` | The pure sim: map generation, actions, projectiles, pickups, zone, win rule, state hash, bot observations | `ecs`, stdlib (no `time`, I/O, net, global RNG) |
| `internal/bot` | The `Controller` interface, the native baseline and random bots, the ONNX policy runner | `world` |
| `internal/sandbox` | Lua bots under CPU and memory limits | `world`, golua |
| `internal/replay` | Match log, encode/decode, re-simulation and verification | `world`, `ecs` |
| `internal/match` | `Game` (one match through controllers), `Manager` (live matches, lobby, bot fill, tick stats) | all of the above |
| `internal/render` | Frame buffer, full and diff encoders, a minimal terminal emulator for tests | `world`, `ecs` |
| `internal/store` | SQLite: accounts, bots, matches with replays, win counts | stdlib, sqlite driver |
| `internal/session` | SSH server, command router, per-session input and output loop | everything |
| `cmd/server`, `cmd/headless`, `cmd/local`, `cmd/loadtest` | Entry points | everything |

The sim does not import anything above it. `go list -deps ./internal/world` shows only `ecs` and the standard library.

## One tick

1. `match.Manager` waits for the 50 ms ticker. This is the only place the clock is read.
2. `Game.Tick` builds an `Observation` for each combatant and calls its `Controller`. A human's controller returns the last key that arrived since the previous tick. Invalid actions become `stay`.
3. `Sim.Step` applies the actions in a fixed order: cooldowns, actions in ascending entity order, projectiles, pickups, zone damage, pickup spawn.
4. The actions are appended to the replay log.
5. A read-only `View` snapshot goes to every subscriber through a one-slot channel. If a session has not consumed the previous snapshot it is replaced, so a slow client drops frames and never slows the match.
6. Each session draws the snapshot into its own frame buffer, diffs it against the frame it sent last, and writes the changed cells.

## Determinism contract

`world.New(Config{Seed})` followed by the same sequence of `Step(inputs)` calls gives the same `Hash()` after every tick.

- All randomness comes from one per-match PCG whose state is part of the hash.
- Iteration is always in ascending entity ID order (sorted ECS stores). The sim never ranges over a Go map.
- Inputs are sorted by entity before they are applied, and the last input per combatant wins, so the order of the input slice does not matter.
- The sim never reads the clock and starts no goroutines.
- The zone is a pure function of the tick number.

Tests: `world.TestStepDeterministic` (20 seeds x 1,000 ticks, shuffled input order) and `match.TestMatchesDeterministicAndReplay` (50 full bot matches run twice, hash compared after every tick, then the log is encoded, decoded and re-simulated to the same hash). Both run in CI under the race detector.

Bots are deterministic too, although replays do not depend on it (decision D9): native bots use seeded RNGs, Lua bots are cut off by instruction count and get a seeded `rand()`, and the policy runner avoids fused multiply-add.

## Concurrency

- One goroutine per live match owns its `Game`; nothing else touches the sim.
- One goroutine pair per session: one reads keys and stores the latest action in an atomic; one receives snapshots and writes frames.
- Lock order is always manager, then match. The manager lock is held only to join, leave or close a match.
- SQLite has a single connection; writes happen once per finished match, off the tick path (during the result screen).

## Persistence

SQLite via the pure-Go driver. Tables: `accounts` (key fingerprint, name, wins, matches), `bots` (per account, up to 8, 16 KB each), `matches` (winner, length, gzipped replay; the last 500 are kept).

## Limits and abuse controls

Session cap, per-address session cap, per-address connection rate limit, idle and absolute timeouts, script size cap, bot count cap, CPU and memory budgets per bot, bounded replay decoding. The container runs as non-root with a read-only root filesystem and a memory limit.
