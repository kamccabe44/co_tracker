data "aws_caller_identity" "current" {}

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  name             = "co-tracker"
  azs              = slice(data.aws_availability_zones.available.names, 0, var.az_count)
  app_fqdn         = "${var.subdomain}.${var.domain_name}"
  image_identifier = "${aws_ecr_repository.app.repository_url}:${var.image_tag}"
}

data "aws_route53_zone" "main" {
  name         = var.domain_name
  private_zone = false
}
