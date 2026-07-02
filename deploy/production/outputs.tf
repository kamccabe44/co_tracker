output "ecr_repository_url" {
  description = "Push the container image here before applying."
  value       = aws_ecr_repository.app.repository_url
}

output "public_url" {
  value = "https://${local.app_fqdn}"
}

output "alb_dns_name" {
  description = "ALB DNS name (works immediately over HTTP, before DNS/ACM finish)."
  value       = "http://${aws_lb.main.dns_name}"
}

output "ecs_cluster_name" {
  value = aws_ecs_cluster.main.name
}

output "ecs_service_name" {
  value = aws_ecs_service.app.name
}

output "efs_file_system_id" {
  value = aws_efs_file_system.data.id
}

output "admin_login" {
  description = "Bootstrap admin login provisioned by Terraform. Sensitive."
  sensitive   = true
  value = {
    login_url = "https://${local.app_fqdn}/login"
    users     = var.users_json
  }
}
