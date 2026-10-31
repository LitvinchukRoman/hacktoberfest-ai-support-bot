data "aws_caller_identity" "current" {}

data "aws_route53_zone" "root" {
  name = var.domain_name
}

# The account has two VPCs tagged naukma-coffee-vpc; the subnet name is unique
# and points at the one that hosts the shared services.
data "aws_subnet" "public" {
  filter {
    name   = "tag:Name"
    values = [var.shared_public_subnet_name]
  }
}

data "aws_ami" "al2023_arm" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["al2023-ami-2023.*-arm64"]
  }
  filter {
    name   = "architecture"
    values = ["arm64"]
  }
  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }
}

data "aws_security_group" "rds" {
  count = var.rds_security_group_name == "" ? 0 : 1
  name  = var.rds_security_group_name
}

locals {
  account_id = data.aws_caller_identity.current.account_id
  fqdn       = "${var.subdomain}.${var.domain_name}"
  ssm_prefix = "/${var.name}/env"
}
