#!/usr/bin/env bash
# Build the Go API for the box (linux/arm64), upload it, roll it out via SSM.
#
#   infrastructure/scripts/deploy.sh                 # builds ./cmd/server from the repo root
#   APP_DIR=backend PKG=./cmd/api infrastructure/scripts/deploy.sh
#
# Release id = HEAD sha, so the working tree must be committed (ALLOW_DIRTY=1 to override).
set -euo pipefail

INFRA_DIR="$(cd "$(dirname "$0")/.." && pwd)"
REPO_DIR="$(cd "$INFRA_DIR/.." && pwd)"
APP_DIR="${APP_DIR:-.}"
PKG="${PKG:-./cmd/server}"
# GitHub Actions gets credentials from OIDC, not from a profile.
[ -n "${CI:-}" ] || export AWS_PROFILE="${AWS_PROFILE:-personal}"
export AWS_REGION="${AWS_REGION:-eu-central-1}"

cd "$REPO_DIR"
if [ -z "${ALLOW_DIRTY:-}" ] && [ -n "$(git status --porcelain -- "$APP_DIR")" ]; then
  echo "uncommitted changes in $APP_DIR; commit first or set ALLOW_DIRTY=1" >&2
  exit 1
fi
SHA="$(git rev-parse HEAD)"

# CI passes these as env; locally they come from terraform output.
tf_out() { terraform -chdir="$INFRA_DIR" output -raw "$1"; }
BUCKET="${RELEASES_BUCKET:-$(tf_out releases_bucket)}"
INSTANCE="${INSTANCE_ID:-$(tf_out instance_id)}"
DOCUMENT="${DEPLOY_DOCUMENT:-$(tf_out deploy_document)}"

OUT="$(mktemp -d)"
trap 'rm -rf "$OUT"' EXIT

echo "==> build $PKG ($SHA)"
(cd "$APP_DIR" && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags "-s -w" -o "$OUT/support-api" "$PKG")
(cd "$OUT" && shasum -a 256 support-api > support-api.sha256)

echo "==> upload s3://$BUCKET/releases/$SHA/"
aws s3 cp --only-show-errors "$OUT/support-api" "s3://$BUCKET/releases/$SHA/support-api"
aws s3 cp --only-show-errors "$OUT/support-api.sha256" "s3://$BUCKET/releases/$SHA/support-api.sha256"

echo "==> deploy to $INSTANCE"
CMD_ID="$(aws ssm send-command \
  --instance-ids "$INSTANCE" \
  --document-name "$DOCUMENT" \
  --parameters "Sha=$SHA" \
  --comment "support-api $SHA" \
  --query Command.CommandId --output text)"

aws ssm wait command-executed --command-id "$CMD_ID" --instance-id "$INSTANCE" || true

aws ssm get-command-invocation --command-id "$CMD_ID" --instance-id "$INSTANCE" \
  --query '[Status, StandardOutputContent, StandardErrorContent]' --output text

STATUS="$(aws ssm get-command-invocation --command-id "$CMD_ID" --instance-id "$INSTANCE" --query Status --output text)"
[ "$STATUS" = "Success" ] || { echo "deploy $STATUS" >&2; exit 1; }
echo "==> deployed $SHA"
