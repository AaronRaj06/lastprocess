# Decisions

Short entries: context, choice, consequences.

## D1. Host on Oracle Cloud Always Free, not Fly.io
- **Context:** The project must cost nothing. As far as we know, Fly.io dropped its free allowance for new accounts in 2024, and SSH on port 22 needs a paid dedicated IPv4 address. Phase 8 will check this again.
- **Choice:** Oracle Cloud Always Free VM, reached through a free dynamic-DNS subdomain until a real domain exists.
- **Consequences:** We run our own VM (Docker, systemd), with no platform-managed deploys. CI/CD deploys over SSH.

## D2. Our own ECS with sorted sparse sets
- **Context:** The sim must be deterministic. Iterating over a Go map, or over a swap-remove dense array, makes the order depend on history.
- **Choice:** One generic `Store[T]` per component type. The dense array is kept sorted by entity index, and a sparse array gives O(1) `Get`. `Each` always visits entities in ascending ID order. There is no scheduler: systems are plain functions called in a fixed order from `Sim.Step`.
- **Consequences:** Set and Remove are O(n). That costs about 1 µs at 400 entities (see benchmarks.md). Entity handles are 24-bit index plus 8-bit generation. A stale handle goes undetected only after 256 reuses of the same slot.

## D3. Contested moves: lower entity ID moves first
- **Context:** Two combatants moving into the same cell need a resolution that never changes.
- **Choice:** `Step` sorts inputs by entity and applies them in that order against an occupancy grid. If a combatant sends several inputs in one tick, the last one wins. Input slice order never matters, and a test enforces this.
- **Consequences:** A combatant that steps into a cell being vacated in the same tick by a higher-ID combatant is blocked. This is a small, known bias. A future fix is simultaneous resolution, if gameplay needs it.

## D4. The fixed-tick loop lives outside `internal/world`
- **Context:** The brief lists a "tick loop" under `internal/world`, but the sim must not read the clock.
- **Choice:** `world` exposes `Step` and the `TickRate` constant (20). Wall-clock pacing lives in the caller: `cmd/local` now, `internal/match` in Phase 2, and the headless runner steps as fast as it can.
- **Consequences:** The sim stays pure and testable. The headless runner needs no changes to run faster than real time.

## D5. Extra entry point `cmd/local`
- **Context:** Phase 1 needs a local TUI, and the layout in the brief has no place for it.
- **Choice:** Add `cmd/local`, a single-player Bubble Tea client.
- **Consequences:** It may be folded into `cmd/server --local` once the shared renderer exists in Phase 2.

## D6. CI workflow lives in `.github/workflows`, not `/deploy`
- **Context:** GitHub only runs workflows from `.github/workflows`.
- **Choice:** CI lives there. `/deploy` will hold the Dockerfile and host config.

## D7. Randomness: `math/rand/v2` PCG, one per match
- **Choice:** `rand.NewPCG(seed, seed^0x9E3779B97F4A7C15)`, owned by the `Sim`. Map generation and spawning draw from it in a fixed order. Its marshalled state is included in `Sim.Hash`, so if the RNG is consumed out of order, the determinism test fails.

## D8. SSH sessions write frames directly; Bubble Tea is used only by the local client
- **Context:** The brief asks for Bubble Tea and for a renderer that sends only changed cells and counts bytes per frame. Bubble Tea's renderer owns the output stream and redraws whole changed lines, so we could not control or measure cell-level diffs through it. It also asks the terminal for its colours at start-up and waits for the answer, which is fragile over SSH.
- **Choice:** `internal/render` produces a grid of cells and emits diffs as raw ANSI. `internal/session` reads key bytes from the SSH channel and writes those diffs. Wish still provides the SSH server, host key handling and rate limiting. `cmd/local` keeps Bubble Tea and draws the same frames.
- **Consequences:** We parse arrow-key escape sequences ourselves (20 lines, tested). A session costs one goroutine pair and no Bubble Tea program. Measured: 85 bytes per frame against 5,656 for a full redraw.

## D9. Replays record every combatant's actions and never re-run the bots
- **Context:** A replay needs the same inputs in the same order. Bots could be re-run, but then a replay would depend on bot code, the Lua VM version and floating-point behaviour of the policy on the replaying machine.
- **Choice:** The log is the seed plus one action byte per combatant per tick, gzipped (a few KB per match). Replay feeds the bytes back into the sim and compares the final state hash.
- **Consequences:** A replay depends only on the sim. Bots are still deterministic (seeded RNG, instruction-counted Lua, FMA-free policy math), and tests check that, but replays do not rely on it.

## D10. Lua sandbox: `arnodel/golua`, not `gopher-lua` (the section 6 spike)
- **Context:** Scripts are hostile. We need a deterministic CPU budget, a memory cap, and no way out of the VM.
- **Spike evidence** (run on 2026-11-16, gopher-lua v1.1.2, golua v0.4.1):
  1. *gopher-lua.* It has no instruction hook, but its VM loop calls `ctx.Done()` exactly once per instruction when a context is set, so a custom `context.Context` that counts those calls is a deterministic instruction counter without forking. We confirmed it: a `while true` loop was stopped after exactly 10,001 polls on two runs with identical state. But one instruction can do unbounded work in Go. With a 1,000-instruction budget, `s = s .. s` in a loop built a **268 MB** string, and one `string.find` with a backtracking pattern ran for **over 2 minutes** (the test was killed at the timeout). gopher-lua has no memory accounting, so fixing this means patching the VM and auditing every library function.
  2. *cgo Lua with `lua_sethook`.* Count hooks are deterministic and `lua_newstate` takes a custom allocator for a hard memory cap. Rejected because cgo would break the static, cross-compiled binary and put C in the same process as hostile input.
  3. *WASM (wazero).* Would need a Lua-to-WASM toolchain or a different bot language. Too much for this project; first on the cut list.
  4. *golua.* A pure-Go Lua 5.4 VM designed for this: a context has hard CPU and memory limits, the CPU unit is a VM tick that library functions also pay for in proportion to their work, allocations are charged before they happen, and Go functions that are not declared CPU/memory/IO safe are refused inside a limited context.
- **Choice:** golua. Each bot gets its own runtime, a whitelisted environment, and a fresh limit context per `act` call (`internal/sandbox`).
- **Consequences:** The same attacks that broke option 1 are stopped after 5 to 35 CPU ticks. 28 adversarial cases pass. A bot tick costs about 15 µs against 14 ns for the native baseline.
- **Known weaknesses:** (a) golua's limits are marked alpha upstream; we pin v0.4.1 and rely on our own attack suite. (b) Memory is counted as bytes allocated, not bytes live, so the cap is a budget, not a heap limit; a bot that produces a lot of garbage gets disqualified even though the GC could have coped. (c) A VM tick is not a fixed amount of machine time, so the CPU budget bounds work only up to a constant factor. (d) A bug in golua itself (a Go panic) is caught by `recover` and counted as a violation, but a Go stack overflow or a runtime-level out-of-memory cannot be caught; the container memory limit is the last line of defence. (e) Bots share the process with the server; there is no OS-level isolation.

## D11. Trainer IPC: one TCP socket, binary frames, all environments per message
- **Options:** gRPC (schema and codegen in two languages), shared memory (fastest, but platform-specific and fiddly), one socket per environment (simple, but one round trip per environment per step).
- **Choice:** `cmd/headless -serve` hosts N environments behind one TCP connection. One request carries N action bytes; one reply carries N observations, rewards and done flags as little-endian arrays that NumPy reads without copying field by field. The whole protocol is about 60 lines per side.
- **Consequences:** About 25,000 environment steps per second with 64 environments on an M1 while PPO is also training. The trainer exposes both a standard `gymnasium.Env` (one match) and an SB3 `VecEnv` (used for training).

## D12. Policy inference: a small ONNX reader in Go, no ONNX Runtime
- **Options:** `onnxruntime_go` (needs cgo and a 30 MB shared library in the image), a Python sidecar (a second process on a 1 GB VM, and network latency inside the tick), or evaluating the network ourselves.
- **Choice:** The policy is exported to standard ONNX. `internal/bot/policy.go` decodes the ONNX protobuf by hand (no protobuf dependency) and supports exactly the operators a multilayer perceptron needs: `Gemm`, `MatMul`, `Add`, `Tanh`, `Relu`, `Flatten`, `Identity`. Anything else is rejected at load. The trained policy is embedded in the binary.
- **Evidence:** `TestPolicyMatchesPyTorch` compares Go logits with PyTorch's on 16 vectors written at export time; the trainer's test compares ONNX Runtime with the same vectors.
- **Consequences:** Static binary, no cgo. Inference is scalar Go, about 90 µs per decision for a 259-256-256-13 network, which is the most expensive bot kind (see benchmarks). Products are rounded to float32 before adding so the compiler cannot fuse them into FMA instructions, which keeps results identical on arm64 and amd64. The reader is not a general ONNX runtime; a convolutional policy would need a different answer.

## D13. Accounts are SSH public keys; guests need no key
- **Choice:** The server accepts any public key and uses its SHA-256 fingerprint as the account. Clients with no key fall through to keyboard-interactive authentication that asks nothing. Guests can play and spectate; uploading bots and the leaderboard need a key.
- **Consequences:** No passwords, no sign-up, and `ssh host` works from a fresh machine. An account cannot be recovered if the key is lost.

## D14. Matches are short and joinable within seconds
- **Choice:** A match lasts at most 90 seconds. One bot-only match always runs. A player who connects joins a match that is in its 3-second lobby, or gets a new match. Empty slots are filled with bots (`-fill`, cycling RL and baseline). After the result screen the same players start the next round.
- **Consequences:** Nobody waits for other humans. Bot-only matches are not stored; matches with a person or an uploaded bot are, and the last 500 are kept.

## D15. RL input features: ray casts added to the vector
- **Context:** With only the raw 15x15 view (as scaled cell codes) the PPO policy plateaued at 44.5% (8M steps) and 47.4% (30M steps) of 2,000 evaluation duels against the baseline.
- **Choice:** `Observation.Vector` also emits, for each direction, the distance to the first wall and the closeness of the first enemy and projectile on that line. These are computed from the view, so the bot API and the information available are unchanged.
- **Consequences:** 47.9% with rays, and 48.5% after also adding the `threats` field (incoming enemy projectiles, available to every bot kind). From scratch the policy reaches parity with the baseline but does not beat it. An imitation warm start on the baseline followed by PPO (ppo-v5) does: 56.0% against 42.4%. The env server gained an `X` op that returns the baseline's action for the learner's current observation, used only for those labels. Details are in the RL section of `docs/benchmarks.md`.
