# This is a single, dedicated stack (not a reusable module like os_alerts'
# multi-instance setup) — co_tracker has exactly one deployment target, so
# there's no "name"/"instance" indirection to carry.

variable "aws_region" {
  description = "Region to deploy in. No CloudFront involved, so unlike os_alerts this isn't pinned to us-east-1 — kept as the default for convenience since that's most likely where the rest of this AWS account already lives."
  type        = string
  default     = "us-east-1"
}

variable "ecr_repo_name" {
  description = "Name of the ECR repository the image is pushed to."
  type        = string
  default     = "co-tracker"
}

variable "image_tag" {
  description = "Image tag to deploy. Push this tag to ECR before applying (see scripts/update.sh)."
  type        = string
  default     = "latest"
}

variable "az_count" {
  description = "Number of AZs (an ALB needs subnets in >= 2)."
  type        = number
  default     = 2
}

variable "container_port" {
  type    = number
  default = 8080
}

variable "fargate_cpu" {
  description = "Task-level CPU units. 256 = 0.25 vCPU — co_tracker idles around 15MB RSS, so this is generous headroom, not a tight fit."
  type        = number
  default     = 256
}

variable "fargate_memory" {
  description = "Task-level memory in MiB. Must be a value valid for fargate_cpu."
  type        = number
  default     = 512
}

variable "use_fargate_spot" {
  description = "Run on Fargate Spot (~70% cheaper) instead of on-demand. Spot tasks can be interrupted with a 2-minute warning and are automatically rescheduled — acceptable for an internal scheduling tool, but means occasional brief unavailability during interruption + restart."
  type        = bool
  default     = false
}

variable "users_json" {
  description = "Bootstrap login accounts (username -> password), seeded once when the accounts table is empty. CHANGE the default before applying."
  type        = string
  sensitive   = true
  default     = "{\"admin\": \"change-me-immediately\"}"
}

# ── Custom domain ─────────────────────────────────────────────────────────────

variable "domain_name" {
  description = "Root domain with an existing Route 53 hosted zone in this AWS account."
  type        = string
  default     = "1136mpco.com"
}

variable "subdomain" {
  description = "Subdomain to serve on: '<subdomain>.<domain_name>'."
  type        = string
  default     = "tracking"
}
