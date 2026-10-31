variable "name" {
  description = "Prefix for resource names, SSM paths and tags."
  type        = string
  default     = "support-bot"
}

variable "aws_region" {
  description = "Same region as the rest of the account (Frankfurt)."
  type        = string
  default     = "eu-central-1"
}

variable "aws_profile" {
  type    = string
  default = "personal"
}

variable "domain_name" {
  description = "Existing Route53 zone; this stack only adds one record."
  type        = string
  default     = "naukma.com"
}

variable "subdomain" {
  type    = string
  default = "support-api"
}

variable "acme_email" {
  description = "Let's Encrypt account email for Caddy."
  type        = string
}

variable "instance_type" {
  description = "One Go binary plus Caddy fit in 512 MiB (with a swap file)."
  type        = string
  default     = "t4g.nano"
}

variable "root_volume_gb" {
  type    = number
  default = 10
}

variable "shared_public_subnet_name" {
  description = "Name tag of the shared public subnet (naukma-coffee VPC) the box launches into."
  type        = string
  default     = "naukma-coffee-public-eu-central-1a"
}

variable "caddy_version" {
  type    = string
  default = "2.11.7"
}

variable "app_port" {
  description = "Loopback port the Go API listens on; only Caddy reaches it."
  type        = number
  default     = 8080
}

variable "health_path" {
  description = "Endpoint the deploy waits for before it keeps a release."
  type        = string
  default     = "/healthz"
}

variable "app_env" {
  description = "Plain API settings, stored as SSM String parameters (model names, feature flags)."
  type        = map(string)
  default = {
    # The local PostgreSQL from bootstrap: unix socket, peer auth, so no password.
    DATABASE_URL         = "postgres:///skelar?host=/run/postgresql&user=support"
    SUPPORT_AUTO_MIGRATE = "true"
    # gemma-4-26b-a4b-it: 98% on the goldset at p95 2.8 s; gemma-4-31b-it was overloaded and slow.
    SUPPORT_CLASSIFIER_MODEL = "gemma-4-26b-a4b-it"
    SUPPORT_WRITER_MODEL     = "gemma-4-26b-a4b-it"
    SUPPORT_LLM_TIMEOUT      = "15s"
  }
}

variable "secret_names" {
  description = "Secret API settings. Terraform creates placeholders; set real values with aws ssm put-parameter (see README)."
  type        = list(string)
  default     = ["GEMINI_API_KEY"]
}

variable "github_repository" {
  type    = string
  default = "LitvinchukRoman/hacktoberfest-ai-support-bot"
}

variable "github_owner_id" {
  description = "Numeric owner id from the immutable OIDC sub prefix."
  type        = number
  default     = 198748414
}

variable "github_repository_id" {
  description = "Numeric repo id from the immutable OIDC sub prefix."
  type        = number
  default     = 1440766956
}

variable "github_environment" {
  type    = string
  default = "prod"
}

variable "github_deploy_branch" {
  type    = string
  default = "main"
}

variable "rds_security_group_name" {
  description = "If set (e.g. fantasm-rds), lets this box reach that RDS on 5432. Empty = no database access."
  type        = string
  default     = ""
}
