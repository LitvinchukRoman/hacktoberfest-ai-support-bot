# API environment. The box reads everything under /support-bot/env on every
# (re)start, so changing a value = put-parameter + restart (see README).

resource "random_password" "app_secret" {
  length  = 48
  special = false
}

locals {
  managed_env = merge(
    {
      ADDR       = "127.0.0.1:${var.app_port}"
      PUBLIC_URL = "https://${local.fqdn}"
    },
    var.app_env,
  )
}

resource "aws_ssm_parameter" "env" {
  for_each = local.managed_env

  name  = "${local.ssm_prefix}/${each.key}"
  type  = "String"
  value = each.value
}

resource "aws_ssm_parameter" "app_secret" {
  name  = "${local.ssm_prefix}/APP_SECRET"
  type  = "SecureString"
  value = random_password.app_secret.result
}

# Placeholders only. Real values never pass through Terraform or its state.
# The host skips any parameter still equal to CHANGE_ME.
resource "aws_ssm_parameter" "secret" {
  for_each = toset(var.secret_names)

  name  = "${local.ssm_prefix}/${each.key}"
  type  = "SecureString"
  value = "CHANGE_ME"

  lifecycle {
    ignore_changes = [value]
  }
}

# Deploys one release: download from S3, verify the checksum, switch the
# symlink, restart, and roll back if the health check does not pass.
resource "aws_ssm_document" "deploy" {
  name            = "${var.name}-deploy"
  document_type   = "Command"
  document_format = "YAML"

  content = yamlencode({
    schemaVersion = "2.2"
    description   = "Deploy a support-api release by git sha"
    parameters = {
      Sha = {
        type           = "String"
        description    = "Full git commit sha of the release"
        allowedPattern = "^[0-9a-f]{40}$"
      }
    }
    mainSteps = [{
      action = "aws:runShellScript"
      name   = "deploy"
      inputs = {
        timeoutSeconds = "300"
        runCommand = split("\n", templatefile("${path.module}/host/deploy-release.sh.tftpl", {
          bucket      = aws_s3_bucket.releases.bucket
          region      = var.aws_region
          app_port    = var.app_port
          health_path = var.health_path
        }))
      }
    }]
  })
}
