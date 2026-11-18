# If the live server is offline

The public server (`ssh lastprocess-arena.duckdns.org`) is a **temporary demo host** on an Azure for Students subscription (see `docs/azure-budget.md`). It may be stopped between demo windows, and it ends when the credit or the college account does. The project does not depend on it. In order of preference:

## 1. Local demo (always works, costs nothing)

With Go installed:
```
make demo                  # server on :2222; RL and scripted bots fill every match
ssh -p 2222 localhost      # in another terminal
```
With only Docker, using the published image:
```
docker run --rm -p 2222:2222 ghcr.io/aaronraj06/lastprocess:latest
ssh -p 2222 localhost
```

## 2. Recorded evidence

- `docs/demo.gif`: a live SSH match, recorded with `docs/demo.tape`.
- `internal/match/testdata/example.replay`: a full 8-bot match (2 RL, 2 scripted, 2 Lua, 2 random), 669 ticks. CI checks that it re-simulates bit-identically on linux/amd64, although it was recorded on darwin/arm64. Run `go test -v -run Golden ./internal/match`.
- `docs/benchmarks.md`: every number with its command, hardware and date.

## 3. On-demand home server

Run the same image on a laptop (option 1), then give it a public address. Many Indian home ISPs put customers behind CGNAT, so plain port forwarding often fails. Check first: if the WAN address on the router differs from the one `curl ifconfig.me` shows, you are behind CGNAT.

| Option | Works behind CGNAT | Notes |
|---|---|---|
| Router port forward of TCP 22 (or 2222) | no | Needs a public IPv4 from the ISP; some sell a static IP |
| IPv6 | yes, if ISP and visitor both have IPv6 | Many visitors' networks are IPv4-only |
| TCP tunnel service (e.g. ngrok TCP, bore, Cloudflare Tunnel) | yes | Raw TCP on port 22 is often paid or needs a client on the visitor's side (Cloudflare needs `cloudflared` for SSH). **None of these has been tested with this project.** |

## 4. Another no-card host (unverified suggestions)

Free trials that might not need a card change often. Check the terms before use, and expect them to last weeks, not months. Moving host changes only the four `DEPLOY_*` GitHub secrets (see `deploy/README.md`); the workflow contains no provider-specific step.

## Moving or shutting down

1. `scripts/backup.sh`: copies the SQLite database and the SSH host key to `backups/` (git-ignored). Restoring the host key keeps players from seeing a "host key changed" warning.
2. Provision the new host per `deploy/README.md`, restore the volume, and update the secrets and the DuckDNS record.
3. Update the address line at the top of the README.
