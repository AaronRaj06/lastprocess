# Deploying

Target: one Oracle Cloud Always Free VM (see `docs/decisions.md` D1). Nothing here costs money.

## One-time VM setup

1. Create an Always Free VM (Ubuntu). In its security list / network security group, open TCP **22** and TCP **2200**.
2. Move the VM's own sshd off port 22 so the game can have it:
   ```
   sudo sed -i 's/^#\?Port .*/Port 2200/' /etc/ssh/sshd_config
   sudo iptables -I INPUT -p tcp --dport 2200 -j ACCEPT && sudo netfilter-persistent save
   sudo systemctl restart ssh
   ```
   Reconnect with `ssh -p 2200` before closing the old session.
3. Install Docker: `curl -fsSL https://get.docker.com | sudo sh && sudo usermod -aG docker $USER`.
4. Copy `deploy/docker-compose.yml` to `~/lastprocess/` on the VM.
5. Point a DNS name at the VM's public IP (a free dynamic-DNS subdomain works).

## Continuous deploy

`.github/workflows/deploy.yml` runs on every `v*` tag: it tests, builds the image, pushes it to GHCR and restarts the service over SSH. Set these repository secrets:

| Secret | Value |
|---|---|
| `DEPLOY_HOST` | VM public IP or DNS name |
| `DEPLOY_USER` | VM login user (e.g. `ubuntu`) |
| `DEPLOY_SSH_KEY` | private key allowed to log in to the VM |

Make the GHCR package public once (GitHub → Packages → lastprocess → visibility) so the VM can pull without credentials.

Release: `git tag v0.1.0 && git push --tags`.

## What persists

The `lastprocess-data` volume holds `lastprocess.db` (accounts, bots, last 500 matches) and `host_ed25519` (the server's SSH identity). Back up with `docker run --rm -v lastprocess-data:/d -v $PWD:/out alpine tar czf /out/backup.tgz -C /d .`.

## Limits in production

Defaults: 256 sessions, 4 per client address, 1 new session per second per address (burst 10), 32 concurrent matches, 10 minute idle timeout. Change them with flags in the compose file (`command:`).
