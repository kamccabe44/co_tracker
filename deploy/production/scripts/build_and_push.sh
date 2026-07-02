#!/usr/bin/env bash
#
# Build the co_tracker container and push it to the ECR repository created by
# Terraform. Fargate runs on x86_64, so we force linux/amd64 (important when
# building on Apple Silicon).
#
# Usage:
#   ./build_and_push.sh [image_tag] [repo_name] [region]
#
# Defaults: tag=latest, repo=co-tracker, region=$AWS_REGION or us-east-1
#
set -euo pipefail

TAG="${1:-latest}"
REPO_NAME="${2:-co-tracker}"
REGION="${3:-${AWS_REGION:-us-east-1}}"

# Repo root is three levels up from deploy/production/scripts/
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../../.." && pwd)"

ACCOUNT_ID="$(aws sts get-caller-identity --query Account --output text)"
REGISTRY="${ACCOUNT_ID}.dkr.ecr.${REGION}.amazonaws.com"
IMAGE="${REGISTRY}/${REPO_NAME}:${TAG}"

echo ">> Logging in to ECR (${REGISTRY})"
aws ecr get-login-password --region "${REGION}" \
  | docker login --username AWS --password-stdin "${REGISTRY}"

echo ">> Building ${IMAGE} (linux/amd64) from ${REPO_ROOT}"
docker build --platform linux/amd64 -t "${IMAGE}" "${REPO_ROOT}"

echo ">> Pushing ${IMAGE}"
docker push "${IMAGE}"

echo ">> Done. Image: ${IMAGE}"
