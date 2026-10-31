# One t4g.nano: Caddy (TLS) in front of the Go API, both under systemd.
# No SSH key: shell access and deploys go through SSM.
resource "aws_instance" "app" {
  ami                    = data.aws_ami.al2023_arm.id
  instance_type          = var.instance_type
  subnet_id              = data.aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.app.id]
  iam_instance_profile   = aws_iam_instance_profile.app.name

  user_data = templatefile("${path.module}/host/bootstrap.sh.tftpl", {
    region        = var.aws_region
    fqdn          = local.fqdn
    acme_email    = var.acme_email
    caddy_version = var.caddy_version
    app_port      = var.app_port
    ssm_prefix    = local.ssm_prefix
  })

  # Capped credits: no unlimited-mode bill if something spins.
  credit_specification {
    cpu_credits = "standard"
  }

  root_block_device {
    volume_type           = "gp3"
    volume_size           = var.root_volume_gb
    encrypted             = true
    delete_on_termination = true
  }

  # IMDSv2 only.
  metadata_options {
    http_endpoint               = "enabled"
    http_tokens                 = "required"
    http_put_response_hop_limit = 1
  }

  tags = { Name = "${var.name}-app" }

  lifecycle {
    # Bootstrap runs once. To re-run it: terraform apply -replace=aws_instance.app
    ignore_changes = [ami, user_data]
  }
}

resource "aws_eip" "app" {
  domain = "vpc"
  tags   = { Name = "${var.name}-app" }
}

resource "aws_eip_association" "app" {
  instance_id   = aws_instance.app.id
  allocation_id = aws_eip.app.id
}

resource "aws_security_group" "app" {
  name        = "${var.name}-app"
  description = "Support API: HTTPS and ACME HTTP from anywhere, no SSH (SSM)"
  vpc_id      = data.aws_subnet.public.vpc_id

  tags = { Name = "${var.name}-app-sg" }
}

resource "aws_vpc_security_group_ingress_rule" "https" {
  security_group_id = aws_security_group.app.id
  description       = "HTTPS"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_ingress_rule" "http" {
  security_group_id = aws_security_group.app.id
  description       = "ACME HTTP-01 and redirect to HTTPS"
  from_port         = 80
  to_port           = 80
  ip_protocol       = "tcp"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.app.id
  description       = "SSM, S3, Gemini API, Lets Encrypt, RDS"
  ip_protocol       = "-1"
  cidr_ipv4         = "0.0.0.0/0"
}

resource "aws_vpc_security_group_ingress_rule" "rds_from_app" {
  count = var.rds_security_group_name == "" ? 0 : 1

  security_group_id            = data.aws_security_group.rds[0].id
  description                  = "PostgreSQL from ${var.name}"
  from_port                    = 5432
  to_port                      = 5432
  ip_protocol                  = "tcp"
  referenced_security_group_id = aws_security_group.app.id
}

resource "aws_route53_record" "app" {
  zone_id = data.aws_route53_zone.root.zone_id
  name    = local.fqdn
  type    = "A"
  ttl     = 300
  records = [aws_eip.app.public_ip]
}
