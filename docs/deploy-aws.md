# Deploying CO Tracker on AWS at the lowest cost

The app is a single container with an embedded SQLite database, so the only
infrastructure it needs is **one small compute instance and a few hundred MB
of persistent disk**. That rules out (and out-prices) RDS, load balancers, and
multi-AZ anything. Options below are ranked by monthly cost (us-east-1,
approximate, excluding free tiers).

## Option 0 — k3s on an existing host (≈ $0/month if you already run one)

If you already run a k3s cluster for another app on this domain (e.g.
os_alerts at `reports.1136mpco.com`, bootstrapped via its
`scripts/k3s-setup.sh`), co_tracker can be a second Helm release on the same
single-node cluster for no additional compute cost — just a new DNS record
and a new Let's Encrypt certificate, both free.

```sh
# 1. DNS: point tracking.1136mpco.com at the same IP as reports.1136mpco.com.

# 2. Edit helm/co-tracker/values-k3s.yaml — set a real admin password.

# 3. Deploy (reuses the cluster's existing Traefik ingress + cert-manager
#    ClusterIssuer, no re-bootstrap needed):
./scripts/k3s-deploy.sh
```

This uses `helm/co-tracker/`, a chart deliberately kept simpler than
os_alerts': no Postgres, no uploads volume — just the app and a 1Gi PVC for
the SQLite file. **The Deployment hardcodes `replicas: 1`**: co_tracker's
SQLite store is a single-writer connection, so unlike a stateless app this
one must never be scaled horizontally without first moving the store off
SQLite. Back up the database with `./scripts/k3s-backup.sh` (uses SQLite's
online backup API through a throwaway pod, since the app image has no shell).

If this is a *fresh* host with no k3s yet, run os_alerts'
`scripts/k3s-setup.sh` first (installs k3s, Helm, cert-manager, and the
`letsencrypt-prod` ClusterIssuer) — it's app-agnostic cluster bootstrap, not
specific to os_alerts.

## Option 1 — Lightsail instance (recommended if starting fresh, ≈ $5/month)

Amazon Lightsail's smallest instances bundle compute, disk, and a generous
transfer allowance for a flat price. The instance's disk persists, so SQLite
just works. This is the cheapest option that is still simple to operate.

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
Lightsail firewall. For HTTPS, put Caddy or the Lightsail load-balancer-free
approach of your choice in front, or use Cloudflare's free tier for TLS.

**Backups**: the whole database is one file. A cron line covers it:

```sh
docker run --rm -v co_tracker_data:/data -v /home/ec2-user/backup:/backup \
  alpine cp /data/co_tracker.db /backup/co_tracker-$(date +%F).db
```

## Option 2 — EC2 `t4g.nano` (≈ $4/month, more knobs)

The absolute cheapest always-on compute: `t4g.nano` (2 vCPU burstable ARM,
0.5 GB RAM) at ~$3.06/month on-demand plus an 8 GB gp3 EBS volume (~$0.64).
Same Docker steps as Lightsail. Build the image with
`docker build --platform linux/arm64` (the Go build is architecture-agnostic;
no code changes needed). Buy a 1-year no-upfront reservation or use spot to
roughly halve it. Choose this over Lightsail only if you're comfortable
managing security groups, EBS, and an Elastic IP yourself.

The nano's 0.5 GB RAM is plenty: the container idles around 10–20 MB.

## Option 3 — ECS on Fargate + EFS (≈ $12+/month, fully managed)

The "container-native" managed option: no instances to patch.

- Push the image to **ECR** (private repo, ~$0.10/GB-month; the image is 14 MB).
- Create an **EFS** filesystem and mount it at `/data` in the task definition
  so SQLite survives task restarts.
- One Fargate task at the minimum size (0.25 vCPU / 0.5 GB) runs ~$9/month
  on-demand, ~$3/month on Fargate Spot (fine here — brief interruptions just
  restart the task and the DB is on EFS).
- Skip the load balancer (that alone is ~$16/month): give the task a public
  IP and point DNS at it, or front it with CloudFront/Cloudflare.
- Point the target-group/container health check at `GET /healthz`.

Note EFS adds ~$0.30/GB-month plus per-request charges — trivial at this scale.

## Option 4 — App Runner (pay-per-use, but no disk)

App Runner pauses billing when idle, which sounds ideal for an internal tool —
but it has **no persistent storage**, so SQLite data would vanish on
deploys/restarts. Only consider it if you later swap the store for RDS or
DynamoDB, at which point the DB costs more than options 1–3 anyway.

## Why this app fits the cheap end

- **One static binary, `FROM scratch`**: 14 MB image, ~15 MB RSS — fits the
  smallest instance sizes AWS sells.
- **SQLite, not a DB server**: no RDS minimum (~$12/month), no connection
  management, one-file backups. WAL mode is enabled; the app serializes writes
  through a single connection, which is more than enough for a team-scale
  scheduling tool.
- **No build-step frontend**: the UI is embedded in the binary; there is no
  S3/CloudFront asset pipeline to pay for or maintain.

**Scaling note**: this design intentionally runs a single writer instance. If
you ever need horizontal scaling or multi-writer HA, swap `internal/store` to
Postgres (the store is isolated behind a small interface-shaped package) and
move to Fargate + Aurora Serverless — but for daily unit tracking that day may
never come.
