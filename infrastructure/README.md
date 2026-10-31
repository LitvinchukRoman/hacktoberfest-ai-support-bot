# infrastructure

The Go support API on one `t4g.nano` in the AWS `personal` account (eu-central-1), at `https://support-api.naukma.com`.

```
internet ──443──▶ Caddy (Let's Encrypt, flush_interval -1 for SSE) ──▶ 127.0.0.1:8080 support-api (systemd)
                                                                           │
                         SSM Parameter Store /support-bot/env/* ◀──────────┘ env on every start
```

- Network: the shared `naukma-coffee` VPC, public subnet `naukma-coffee-public-eu-central-1a`, an Elastic IP.
- DNS: an A record in the existing `naukma.com` zone. The zone itself belongs to another stack.
- Access: SSM Session Manager only. There is no SSH key and port 22 is closed.
- Releases: `s3://support-bot-releases-<account>/releases/<git sha>/`. The `support-bot-deploy` SSM document verifies the checksum, switches the release, waits for `/healthz` and rolls back on failure.
- State: `s3://naukma-coffee-terraform-state-v2/support-bot/terraform.tfstate`.

Cost: about $3.0/mo for `t4g.nano`, $0.8 for 10 GB gp3 and $3.6 for the public IPv4. Roughly $7–8/mo in total. PostgreSQL runs on the same box, so it adds nothing.

## Bring it up

```bash
cd infrastructure
cp terraform.tfvars.example terraform.tfvars   # set acme_email
terraform init -reconfigure -backend-config=backend.hcl
terraform plan -out=tfplan
terraform apply tfplan
```

Caddy gets the certificate on its own once DNS resolves to the EIP. That takes 1–3 minutes after apply.

## Secrets

Terraform creates `/support-bot/env/*` placeholders with the value `CHANGE_ME`. The box skips them until they get real values. Real values never pass through Terraform or its state:

```bash
aws ssm put-parameter --profile personal --region eu-central-1 --overwrite \
  --name /support-bot/env/GEMINI_API_KEY --type SecureString --value '...'
```

Take the key from a Google Cloud project with billing enabled. On the free Gemini API tier, Google may use prompts to improve its products.

Terraform owns `ADDR=127.0.0.1:8080`, `PUBLIC_URL=https://support-api.naukma.com`, the random `APP_SECRET`, `DATABASE_URL` of the local PostgreSQL and the model settings: Gemma 4 through the Gemini API, `gemma-4-26b-a4b-it` as classifier and writer. Without `GEMINI_API_KEY` the API still runs, on the offline keyword classifier. Add plain settings through `app_env` and secrets through `secret_names` in `terraform.tfvars`.

The model is not hosted here. Even the smallest Gemma 4 needs about 3 GB of GPU memory, and this box has 512 MB of RAM and no GPU.

After you change a value, restart the API. It re-reads SSM on start:

```bash
aws ssm send-command --profile personal --region eu-central-1 \
  --instance-ids "$(terraform output -raw instance_id)" \
  --document-name AWS-RunShellScript --parameters 'commands=["systemctl restart support-api"]'
```

## Deploy

The API must listen on `$ADDR` and answer `200` on `GET /healthz`.

```bash
infrastructure/scripts/deploy.sh                          # go build ./cmd/server from the repo root
APP_DIR=backend PKG=./cmd/api infrastructure/scripts/deploy.sh
```

The script builds `linux/arm64` with `CGO_ENABLED=0`, uploads to S3 by `HEAD` sha, and runs the deploy document. It exits non-zero if the health check failed and the box rolled back.

## Operate

```bash
aws ssm start-session --profile personal --region eu-central-1 --target "$(terraform output -raw instance_id)"
sudo journalctl -u support-api -f
sudo journalctl -u caddy -f
sudo cat /var/log/support-bootstrap.log
```

`user_data` runs only at first boot and Terraform ignores changes to it. To re-run the bootstrap (new Caddy version, Caddyfile change), recreate the box. The EIP and DNS stay:

```bash
terraform apply -replace=aws_instance.app
```

## Database

PostgreSQL 16 runs on the box itself (bootstrap installs it): database `skelar`, role `support`, unix socket only, peer auth, no password. `DATABASE_URL` is therefore a plain setting in `app_env`, and the API applies the embedded migrations on start (`SUPPORT_AUTO_MIGRATE=true`).

The data lives on the root volume. There are no backups, and `terraform apply -replace=aws_instance.app` deletes it with the box. Dump it first:

```bash
sudo -u postgres pg_dump skelar | gzip > /tmp/skelar.sql.gz
```

To move to the shared fantasm RDS instead, set `rds_security_group_name = "fantasm-rds"` (opens 5432 from this box), create a separate database and user there, and put that `DATABASE_URL` into SSM as a secret.
