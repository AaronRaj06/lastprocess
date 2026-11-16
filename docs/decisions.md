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
