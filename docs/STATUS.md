# Status audit

Audit of the repo against `CLAUDE_CODE_PROMPT.md` (the original brief), on 2026-11-18 at commit `8126637`, on an Apple M1 (macOS, Go 1.27.4). Commands were run for this audit unless marked "from benchmarks.md" (measured 2026-11-16, same machine).

## Checks run today

| Check | Result |
|---|---|
| `go vet ./...` | pass |
| `golangci-lint run` | 0 issues |
| `cd trainer && uv run pytest -q` | 5 passed |
| `go test -race -cover ./...` | **FAIL**: `internal/sandbox` `TestAttacksContained/growth_across_ticks`. Every other package passes. |

The failure: the "growth across ticks" attack **is contained** (the bot is disqualified with `match allocation budget exceeded (16783278 bytes)`), but under `-race` the subtest takes 6.0 to 11.0 s, and the test has a 5 s wall-clock limit (`internal/sandbox/sandbox_test.go:83`). Without `-race` the whole package passes in 1.2 s. It failed in 4 of 4 runs under `-race`, so CI (`go test -race` on slower GitHub runners) would be red. The fix belongs in the test (a per-`Act` time limit, or a looser limit under race), not in the sandbox. Must be fixed before any deploy tag.

## Phases

| Phase | State | Evidence |
|---|---|---|
| 1 Engine foundation | done | `internal/ecs` (97.3% coverage, movement bench 209 to 218 M entities/s), `internal/world` seeded BSP map + same-seed test, `cmd/local` TUI, CI workflow |
| 2 SSH multiplayer | done | `internal/session` (wish), shared match, 20 Hz tick in `internal/match`, diff renderer `internal/render` (5,631 B to 84 B per frame), `spectate` command and test |
| 3 Combat, zone, replays | done | combat, dash, pickups, zone in `internal/world`; `internal/replay`; `replay <id>` command; determinism test of 50 matches with a hash every tick, run in CI |
| 4 Headless + baseline | done | `cmd/headless`, `internal/bot` baseline/random, 799 matches/s; baseline vs random 80.3% (n = 5,000) |
| 5 RL pipeline | done | `trainer/` (Gym env over TCP, PPO, ONNX export), pure-Go ONNX reader `internal/bot`, ppo-v5 beats the baseline 56.0% to 42.4% (n = 2,000 held-out seeds); server fills with `-fill rl,baseline` |
| 6 Lua sandbox | done | `internal/sandbox` on `arnodel/golua` (D10), deterministic CPU + allocation budgets, size cap, `upload-bot` + `play <bot>`, `bots/examples` (3 bots) |
| 7 Hardening, load, benchmarks | partial | 28-case attack suite (contained, but red under `-race`, see above); load test 1,600 sessions stable on M1. **Not measured on the 1 vCPU / 1 GiB target VM.** |
| 8 Deploy and polish | partial | Dockerfile (24.4 MB, distroless, non-root, read-only), compose, tag deploy workflow, leaderboard, rate/connection limits (`-max-sessions 256`, `-max-per-ip 4`, `-conn-rate 1`), host key + SQLite on `/data` volume, README, demo GIF. **Missing:** public deploy; the workflow and docs are Oracle-specific (hardcoded port 2200, `ubuntu` user, amd64-only build); README has a placeholder `ssh` address. |

## Benchmark table (brief section 8)

| Metric | Measured? | Where / command |
|---|---|---|
| Max concurrent matches and sessions at 20 Hz | yes, M1 only: 1,600 sessions / 201 matches | `make loadtest` |
| p50 / p99 input-to-frame latency | yes, M1 only: 26 / 50 ms | `make loadtest` |
| Bytes per frame, full vs diff | yes: 5,631 B vs 84 B (98.5% less) | `go test -run '^$' -bench FrameBytes ./internal/render` |
| Tick cost per combatant: native / RL / Lua | yes, M1 only: 8.8 ns / 94 µs / 13.7 µs | `go test -run '^$' -bench 'Act' ./internal/bot ./internal/sandbox` |
| ECS entities/s | yes, M1 only | `go test -run '^$' -bench Movement ./internal/ecs` |
| RL win rate vs baseline (n stated) | yes: 56.0% (n = 2,000) | `make eval` |
| Determinism seeds in CI | yes: 50 matches + 20 shuffled-input seeds | `go test -race -run Deterministic ./internal/match ./internal/world` |
| Sandbox attack cases contained | yes: 28 (test red under `-race` for timing only) | `go test -v -run 'Attacks\|Blocked' ./internal/sandbox` |
| Coverage sim / ecs / sandbox | yes: world 91.2%, ecs 97.3%, sandbox 95.9% | `make cover` |
| Any number on the target VM (B1s) | **no** | to do after provisioning |
| Server memory | rough only: 19 MB RSS, `bin/server -fill rl,baseline`, one SSH session, 15 s, M1 (today, not yet in benchmarks.md) | `ps -o rss=` |

## Open risks

- **Red CI**: the sandbox timing test above.
- **Determinism across architectures**: the hash tests run on the dev machine (arm64) and in CI (amd64) separately; no test compares a hash recorded on one architecture with a replay on the other. The ONNX reader avoids FMA fusion for this reason (D12), but nothing checks it end to end. The VM is amd64.
- **Sandbox**: golua's budgets are deterministic but approximate (allocation accounting is per VM operation, not exact bytes). Known weaknesses are in the README; the suite covers 28 cases, not every escape.
- **RL evaluation**: one training seed, final checkpoint only, duel-trained; in 8-player matches the edge is small (16.9% vs 12.5% chance).
- **Memory on 1 GiB**: the server is small (about 19 MB measured roughly), and the compose file caps the container at 512 MB. Docker itself and the OS take most of the RAM; swap is planned. RL inference is pure Go (no ONNX Runtime), so it adds nothing. Needs a real measurement on the VM under load.
- **CPU on 1 vCPU (burstable B1s)**: B-series earn CPU credits at a baseline of about 10% of one vCPU; sustained load beyond that gets throttled. The load-test limit on the VM will be far below the M1 number.
- **No remote**: the repo has no git remote, and `gh` and `az` are not installed on the dev machine, so CI has never run on GitHub and no secrets exist.

## Update 2026-11-18 (after the audit)

Done:
- Sandbox attack test fixed to bound the slowest single call; `go test -race ./...` passes locally and in GitHub CI (run 44318869007, linux/amd64).
- Deploy is host-neutral (four `DEPLOY_*` secrets, set), the image builds for amd64 and arm64, and D16 records the move from Oracle to Azure.
- Azure VM `Standard_B2pts_v2` (Arm, 2 vCPU, 1 GiB) in East Asia. B1s is blocked in every region the subscription's policy allows; see `docs/azure-budget.md`. sshd is on 2200 only, Docker and 2 GiB swap are installed, and a reboot was tested.
- VM benchmarks are in `docs/benchmarks.md`: stable to 800 sessions, and all suites pass on the VM.
- Cross-architecture determinism: a committed replay recorded on arm64 reproduces on amd64 in CI, bots included.
- Budget doc, cost-check, stop, start and backup scripts, `docs/FALLBACK.md`, `make demo`, and README live-host and maintainer notes.

## What remains

1. Point `lastprocess-arena.duckdns.org` at `20.205.110.50` (owner, DuckDNS web UI).
2. First deploy: tag `v0.1.0` (needs approval), make the GHCR package public, and test `ssh` from another network.
3. Run `scripts/backup.sh`, `scripts/azure-stop.sh` and `scripts/azure-start.sh` once against the live deploy.
4. Log the first credit reading from the portal.
