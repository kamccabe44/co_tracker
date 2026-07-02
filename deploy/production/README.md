# Production deployment — ECS Fargate + EFS, self-contained single task

Fully managed deployment: one ECS Fargate task, one container, the existing
embedded-SQLite design unchanged — no separate database service to run. The
SQLite file lives on an EFS volume (Fargate has no local persistent disk), so
the whole app, data included, is still one deployable unit; there's just
nothing left on a server for you to patch.

This mirrors the shape of os_alerts' `deploy/production` Terraform (ECS
Fargate + ALB), but simpler: no Aurora, no S3 uploads bucket, no CloudFront —
CloudFront exists in that stack for App Runner-specific reasons that don't
apply to a plain ALB, which Route 53 can alias directly.

## What this provisions

VPC (2 AZs, no NAT gateway) · ECS Fargate service (1 task, hardcoded — see
below) · Application Load Balancer · EFS (SQLite data) · ACM certificate ·
Route 53 alias record · CloudWatch log group · SSM SecureString for
`USERS_JSON` · IAM roles · ECR repository.

**Not scaled beyond 1 task, and not a values knob.** `internal/store` keeps a
single SQLite connection (WAL mode, one writer). A second concurrent task
would corrupt the database, so `desired_count = 1` is fixed in `ecs.tf`, there
is no auto-scaling target, and deployments are configured to stop the old task
before starting the new one.

## Prerequisites

- Terraform >= 1.6, AWS CLI v2 (authenticated), Docker
- **`1136mpco.com` must already be a Route 53 hosted zone in this AWS
  account.** Check with `aws route53 list-hosted-zones-by-name --dns-name
  1136mpco.com`. If it isn't (e.g. the zone lives at a registrar instead), you
  need to migrate it to Route 53 first, or change `domain_name` in
  `terraform.tfvars` to a domain that is.

## Deploy (one shot)

```bash
cd deploy/production
cp terraform.tfvars.example terraform.tfvars   # edit: set a real users_json
terraform init

# 1) Create the ECR repo, then push the image to it
terraform apply -target=aws_ecr_repository.app
./scripts/build_and_push.sh latest

# 2) Everything else — VPC, EFS, ECS, ALB, ACM, DNS
terraform apply
```

First-time waits: ACM DNS validation a few minutes, EFS mount targets under a
minute. The ALB DNS name works immediately over HTTP for smoke-testing:

```bash
terraform output alb_dns_name
```

Once DNS + ACM finish:

```bash
terraform output public_url          # https://tracking.1136mpco.com
terraform output -json admin_login   # login URL + bootstrap credentials
```

## Updating the app

```bash
./scripts/update.sh            # tags with the git short SHA, prompts before apply
./scripts/update.sh -y         # unattended (skip the Terraform approval)
```

Rollback is just re-deploying a previous tag: `./scripts/update.sh <previous-tag> -y`.

## Backups

The SQLite file is one file on EFS. EFS itself replicates across AZs, but
that's not a substitute for point-in-time backups — take an actual snapshot:

```bash
aws efs create-backup-vault --backup-vault-name co-tracker-backups --region us-east-1  # once
aws backup start-backup-job \
  --backup-vault-name co-tracker-backups \
  --resource-arn "$(terraform output -raw efs_file_system_id | xargs -I{} echo arn:aws:elasticfilesystem:us-east-1:$(aws sts get-caller-identity --query Account --output text):file-system/{})" \
  --iam-role-arn <a role with AWSBackupServiceRolePolicyForBackup>
```

Or simpler: enable [AWS Backup's EFS automatic backup](https://docs.aws.amazon.com/efs/latest/ug/awsbackup.html)
on the file system once via the console — daily snapshots, no cron to maintain.

## Rough monthly cost (us-east-1, low traffic)

| Item | Est. |
|---|---|
| ALB (base + LCU) | ~$17-19 |
| Fargate (0.25 vCPU / 512 MiB, on-demand) | ~$9 |
| Fargate (same, Spot — `use_fargate_spot = true`) | ~$3 |
| EFS (a few hundred MB of SQLite data) | < $1 |
| ACM cert, Route 53 alias query, CloudWatch logs | ~$1 |
| **Total (on-demand)** | **~$27-30/mo** |
| **Total (Fargate Spot)** | **~$21-24/mo** |

The ALB is the dominant cost and is fixed regardless of traffic — it's also
the price of "no server, ever" here. See the repo root `docs/deploy-aws.md`
for cheaper options that trade a small amount of hands-on upkeep for a much
lower bill (a $5-7/month VM with `restart: unless-stopped` and unattended
security updates).

## Caveats

1. Fargate Spot (`use_fargate_spot = true`) can interrupt the task with a
   2-minute warning; ECS reschedules it automatically, but expect an
   occasional ~1-2 minute gap in availability.
2. State is local by default — configure the S3 backend in `versions.tf` if
   more than one person needs to run `terraform apply`.
3. `desired_count` is intentionally not a variable. Don't add one without
   first moving the store off SQLite (see `internal/store`).

## Teardown

```bash
terraform destroy
```

The EFS file system has no deletion protection here — snapshot it first (see
Backups above) if the data matters.
