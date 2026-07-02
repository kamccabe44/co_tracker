# Deploying CO Tracker on AWS

The app is a single container with an embedded SQLite database — the whole
app, data included, is one deployable unit. Options below trade off cost
against server upkeep; pick the point on that spectrum that fits.

## Current: ECS Fargate + EFS (fully managed, ≈ $27-30/month)

**This is what's deployed.** See **[deploy/production/README.md](../deploy/production/README.md)**
for the full Terraform stack: one Fargate task (no auto-scaling — SQLite is
single-writer, so this is intentionally not horizontally scaled), an EFS
volume so the SQLite file survives task restarts/redeploys, an ALB, and a
Route 53 alias at `tracking.1136mpco.com`.

No server to patch, ever — the tradeoff is the ALB's fixed ~$17-19/month,
which dominates the bill regardless of traffic. Run `use_fargate_spot = true`
in `terraform.tfvars` to cut the compute portion by ~70% (occasional brief
interruptions, auto-rescheduled).

This mirrors os_alerts' own `deploy/production` Terraform (ECS Fargate + ALB),
minus the parts specific to that app: no Aurora (co_tracker keeps SQLite — see
"Why this app fits the cheap end" below), no S3 uploads bucket, no CloudFront
(a plain ALB can be aliased directly from Route 53; CloudFront in os_alerts'
stack exists for App Runner-specific reasons that don't apply here).

> AWS App Runner (previously the simplest managed-container option) stopped
> accepting new customers April 30, 2026. AWS's suggested replacement, **ECS
> Express Mode**, wasn't used here: it always provisions its own ALB and only
> supports a single container at creation time, so adding the EFS-backed
> volume this app needs means editing the generated task definition by hand
> anyway — at that point, hand-written Terraform (what's in `deploy/production/`)
> gives the same result with no less effort and much more control.

## Cheaper alternatives, if a small amount of server upkeep is acceptable

### Lightsail instance (≈ $5/month)

Amazon Lightsail's smallest instances bundle compute, disk, and a generous
transfer allowance for a flat price. The instance's disk persists, so SQLite
just works.

```sh
# 1. Create the smallest Lightsail instance (Amazon Linux 2023), then SSH in.
sudo dnf install -y docker && sudo systemctl enable --now docker

# 2. Get the image there. Either push to ECR / Docker Hub and pull, or for a
#    one-off, build on the instance:
git clone https://github.com/kamccabe44/co_tracker && cd co_tracker
sudo docker build -t co_tracker .

# 3. Run it with a named volume for the database:
sudo docker run -d --name co_tracker --restart unless-stopped \
  -p 80:8080 -v co_tracker_data:/data co_tracker
```

Attach a Lightsail static IP (free while attached) and open port 80 in the
Lightsail firewall. For HTTPS, put Caddy in front (auto-provisions Let's
Encrypt with zero config), or use Cloudflare's free tier for TLS. Enable
`unattended-upgrades` for hands-off OS security patching — combined with
`--restart unless-stopped`, ongoing touch time is close to zero.

**Backups**: the whole database is one file. A cron line covers it:

```sh
docker run --rm -v co_tracker_data:/data -v /home/ec2-user/backup:/backup \
  alpine cp /data/co_tracker.db /backup/co_tracker-$(date +%F).db
```

### EC2 `t4g.nano` (≈ $4/month, more knobs)

The absolute cheapest always-on compute: `t4g.nano` (2 vCPU burstable ARM,
0.5 GB RAM) at ~$3.06/month on-demand plus an 8 GB gp3 EBS volume (~$0.64) —
note AWS now also charges ~$3.65/month for a public IPv4 address on EC2,
which puts this close to Lightsail's flat rate anyway. Same Docker steps as
Lightsail. Build the image with `docker build --platform linux/arm64` (the Go
build is architecture-agnostic; no code changes needed). Choose this over
Lightsail only if you're comfortable managing security groups, EBS, and an
Elastic IP yourself.

## k3s on a shared host (currently unused — no k3s host is running)

`helm/co-tracker/` and `scripts/k3s-deploy.sh` / `scripts/k3s-backup.sh` still
exist in this repo: if a k3s cluster is ever running again for another app on
this domain, co_tracker can ride on it as a second Helm release for close to
$0 additional cost (just a new DNS record and a free Let's Encrypt cert). See
the chart's `values-k3s.yaml` and `README.md`'s git history for the original
walkthrough. Not applicable right now — the box this was written for was
decommissioned in favor of the ECS deployment above.

## Why this app fits the cheap end

- **One static binary, `FROM scratch`**: 14 MB image, ~15 MB RSS — fits the
  smallest instance sizes AWS sells, and the smallest Fargate task size with
  room to spare.
- **SQLite, not a DB server**: no RDS/Aurora minimum, no connection pool to
  tune, one-file backups. WAL mode is enabled; the app serializes writes
  through a single connection, which is more than enough for a team-scale
  scheduling tool.
- **No build-step frontend**: the UI is embedded in the binary; there is no
  S3/CloudFront asset pipeline to pay for or maintain.

**Scaling note**: this design intentionally runs a single writer instance —
`desired_count` is hardcoded, not a Terraform variable, for exactly this
reason. If you ever need horizontal scaling or multi-writer HA, swap
`internal/store` to Postgres (the store is isolated behind a small
interface-shaped package) first — but for daily unit tracking that day may
never come, and doing it prematurely trades away the "one container, no
separate database" simplicity for no benefit.
