#!/usr/bin/env bash
# k3s-deploy.sh — Build the co_tracker image and deploy/upgrade on k3s.
#
# Assumes k3s + Helm + cert-manager + a "letsencrypt-prod" ClusterIssuer are
# already installed on this host (true if you've deployed os_alerts here via
# its scripts/k3s-setup.sh). See docs/deploy-aws.md if this is a fresh host.
#
# Run from the repo root:
#   ./scripts/k3s-deploy.sh
#
# Re-running is safe — it rebuilds the image and does a helm upgrade.

set -euo pipefail

RELEASE="co-tracker"
NAMESPACE="co-tracker"
CHART="./helm/co-tracker"
VALUES="./helm/co-tracker/values-k3s.yaml"
IMAGE="co-tracker"
TAG="latest"

log()  { printf '\033[1;32m[%s]\033[0m %s\n'       "$(date '+%H:%M:%S')" "$*"; }
warn() { printf '\033[1;33m[%s] WARN:\033[0m %s\n' "$(date '+%H:%M:%S')" "$*"; }
die()  { printf '\033[1;31m[ERROR]\033[0m %s\n' "$*" >&2; exit 1; }
step() { printf '\n\033[1;34m── %s\033[0m\n' "$*"; }

[[ -f go.mod ]]             || die "Run from the repo root"
[[ -f "$VALUES" ]]          || die "Values file not found: $VALUES"
command -v docker  &>/dev/null || die "Docker not found — run os_alerts' k3s-setup.sh first"
command -v helm    &>/dev/null || die "Helm not found — run os_alerts' k3s-setup.sh first"
command -v kubectl &>/dev/null || die "kubectl not found — run os_alerts' k3s-setup.sh first"

if grep -q 'change-me-before-deploying' "$VALUES"; then
    die "Set a real admin password in ${VALUES} (app.auth.users.admin) before deploying."
fi

export KUBECONFIG="${KUBECONFIG:-$HOME/.kube/config}"

# ── Step 1: Build image ───────────────────────────────────────────────────────
step "1/4  Building Docker image ${IMAGE}:${TAG}"
docker build --platform linux/amd64 -t "${IMAGE}:${TAG}" .
log "Image built."

# ── Step 2: Import image into k3s containerd ──────────────────────────────────
step "2/4  Importing image into k3s (containerd)"
docker save "${IMAGE}:${TAG}" | sudo k3s ctr images import -
log "Image imported: $(sudo k3s ctr images ls | grep "${IMAGE}:${TAG}" | awk '{print $1}')"

# ── Step 3: Helm upgrade --install ────────────────────────────────────────────
step "3/4  Helm upgrade --install"
helm upgrade "$RELEASE" "$CHART" \
    --namespace "$NAMESPACE" \
    --create-namespace \
    --install \
    --values "$VALUES" \
    --wait \
    --timeout 5m

log "Helm release '$RELEASE' deployed to namespace '$NAMESPACE'."

# The k3s values use imagePullPolicy: Never with the mutable ':latest' tag, so
# a helm upgrade alone leaves the pod spec unchanged and the running pod keeps
# the OLD image. Restart it to pick up what was just imported.
APP_DEPLOY="${RELEASE}-co-tracker"   # matches Helm fullname: <release>-<chart>
kubectl rollout restart "deployment/${APP_DEPLOY}" -n "$NAMESPACE"
kubectl rollout status  "deployment/${APP_DEPLOY}" -n "$NAMESPACE" --timeout 5m
log "App deployment '${APP_DEPLOY}' rolled out with the new image."

# ── Step 4: Verify ────────────────────────────────────────────────────────────
step "4/4  Verification"
kubectl get pods -n "$NAMESPACE"
kubectl get pvc -n "$NAMESPACE"
kubectl get ingress -n "$NAMESPACE"

DOMAIN=$(grep 'host:' "$VALUES" | awk '{print $2}')
CERT_STATUS=$(kubectl get certificate -n "$NAMESPACE" co-tracker-tls \
    -o jsonpath='{.status.conditions[0].reason}' 2>/dev/null || echo "pending")

printf '\n'
log "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
log " Deployment complete!"
log ""
log " URL  : https://${DOMAIN}"
log " TLS  : ${CERT_STATUS} (may take 1-2 min for first cert)"
log ""
log " Useful commands:"
log "   kubectl get pods -n ${NAMESPACE}"
log "   kubectl logs -n ${NAMESPACE} -l app.kubernetes.io/name=co-tracker -f"
log "   kubectl get certificate -n ${NAMESPACE}"
log "   ./scripts/k3s-backup.sh"
log ""
log " To update after a code change:"
log "   git pull && ./scripts/k3s-deploy.sh"
log "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
