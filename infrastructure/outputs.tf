output "url" {
  value = "https://${local.fqdn}"
}

output "public_ip" {
  value = aws_eip.app.public_ip
}

output "instance_id" {
  value = aws_instance.app.id
}

output "releases_bucket" {
  value = aws_s3_bucket.releases.bucket
}

output "deploy_document" {
  value = aws_ssm_document.deploy.name
}

output "ssm_env_prefix" {
  value = local.ssm_prefix
}

output "github_deploy_role_arn" {
  value = aws_iam_role.github_deploy.arn
}

# Run once after apply (and again after `-replace=aws_instance.app`).
output "github_setup_commands" {
  value = join("\n", [
    "gh api -X PUT repos/${var.github_repository}/environments/${var.github_environment} -F 'deployment_branch_policy[protected_branches]=false' -F 'deployment_branch_policy[custom_branch_policies]=true'",
    "gh api -X POST repos/${var.github_repository}/environments/${var.github_environment}/deployment-branch-policies -f name=${var.github_deploy_branch}",
    "gh variable set AWS_DEPLOY_ROLE_ARN --repo ${var.github_repository} --env ${var.github_environment} --body ${aws_iam_role.github_deploy.arn}",
    "gh variable set RELEASES_BUCKET --repo ${var.github_repository} --env ${var.github_environment} --body ${aws_s3_bucket.releases.bucket}",
    "gh variable set DEPLOY_DOCUMENT --repo ${var.github_repository} --env ${var.github_environment} --body ${aws_ssm_document.deploy.name}",
    "gh variable set INSTANCE_ID --repo ${var.github_repository} --env ${var.github_environment} --body ${aws_instance.app.id}",
  ])
}

output "shell" {
  value = "aws ssm start-session --target ${aws_instance.app.id} --profile ${var.aws_profile} --region ${var.aws_region}"
}
