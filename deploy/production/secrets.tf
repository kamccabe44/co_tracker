locals {
  ssm_prefix = "/${local.name}/production"
}

resource "aws_ssm_parameter" "users_json" {
  name        = "${local.ssm_prefix}/USERS_JSON"
  description = "Bootstrap login accounts (username -> password)"
  type        = "SecureString"
  value       = var.users_json
}
