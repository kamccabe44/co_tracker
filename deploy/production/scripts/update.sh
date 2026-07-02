#!/usr/bin/env bash
#
# Update the deployed application.
#
# Builds a fresh container image, pushes it to ECR under a unique tag, and
# rolls the ECS service to it via Terraform. Using Terraform (rather than a
# bare `aws ecs update-service --force-new-deployment`) means any config/env
# changes get applied in the same step, and tagging per-build (instead of
# reusing ':latest') gives a clean rollback path: just re-apply the previous
# tag.
#
# Usage:
#   ./update.sh [tag] [-y|--yes]
#
#   tag        Image tag to build/deploy. Defaults to the current git short SHA
#              (suffixed if the tree is dirty), or a UTC timestamp outside git.
#   -y, --yes  Skip the interactive Terraform approval (for CI / unattended use).
#
# Prereqs: AWS CLI (authenticated), Docker, Terraform. Region defaults to
# $AWS_REGION or us-east-1.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TF_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"        # deploy/production
REPO_ROOT="$(cd "${TF_DIR}/../.." && pwd)"
REGION="${AWS_REGION:-us-east-1}"

# ── Parse args ────────────────────────────────────────────────────────────────
AUTO=""
TAG=""
for arg in "$@"; do
  case "$arg" in
    -y|--yes) AUTO="-auto-approve" ;;
    -h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
    -*) echo "Unknown option: $arg" >&2; exit 2 ;;
    *)  TAG="$arg" ;;
  esac
done

# ── Decide the image tag ──────────────────────────────────────────────────────
if [[ -z "$TAG" ]]; then
  if git -C "$REPO_ROOT" rev-parse --short HEAD >/dev/null 2>&1; then
    TAG="$(git -C "$REPO_ROOT" rev-parse --short HEAD)"
    if ! git -C "$REPO_ROOT" diff --quiet || ! git -C "$REPO_ROOT" diff --cached --quiet; then
      TAG="${TAG}-dirty-$(date -u +%Y%m%d%H%M%S)"
    fi
  else
    TAG="$(date -u +%Y%m%d%H%M%S)"
  fi
fi

echo "==> Updating to image tag: ${TAG}"

# ── 1) Build + push the image ────────────────────────────────────────────────
"${SCRIPT_DIR}/build_and_push.sh" "${TAG}"

# ── 2) Roll the service via Terraform (code + any config/env changes) ────────
cd "${TF_DIR}"
terraform init -input=false >/dev/null
terraform apply ${AUTO} -var="image_tag=${TAG}"

# ── 3) Report status + URL ────────────────────────────────────────────────────
echo
echo "==> Deployed tag ${TAG}"

CLUSTER="$(terraform output -raw ecs_cluster_name 2>/dev/null || true)"
SERVICE="$(terraform output -raw ecs_service_name 2>/dev/null || true)"
if [[ -n "$CLUSTER" && -n "$SERVICE" ]]; then
  echo "==> Waiting for the service to become stable..."
  aws ecs wait services-stable --region "${REGION}" --cluster "${CLUSTER}" --services "${SERVICE}"
  STATUS="$(aws ecs describe-services --region "${REGION}" --cluster "${CLUSTER}" --services "${SERVICE}" \
              --query 'services[0].deployments[0].rolloutState' --output text 2>/dev/null || echo '?')"
  echo "    ${SERVICE}: ${STATUS}"
fi

URL="$(terraform output -raw public_url 2>/dev/null || true)"
[[ -n "$URL" ]] && echo "Public URL: ${URL}"

echo
echo "Rollback if needed:  ./update.sh <previous-tag> -y"
