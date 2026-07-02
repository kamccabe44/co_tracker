#!/usr/bin/env bash
# k3s-backup.sh — Snapshot the co_tracker SQLite database from the k3s PVC.
#
# The app image is `FROM scratch` (no shell, no sqlite3 CLI), so this runs a
# throwaway alpine pod that mounts the same PVC read-only and uses SQLite's
# online backup API (`.backup`), which is WAL-safe even while the app is
# actively writing — a raw file copy of co_tracker.db could miss data still
# sitting in co_tracker.db-wal.
#
# Usage: ./scripts/k3s-backup.sh [output-file]

set -euo pipefail

NAMESPACE="co-tracker"
OUT="${1:-co_tracker-backup-$(date +%F-%H%M%S).db}"

command -v kubectl &>/dev/null || { echo "kubectl not found" >&2; exit 1; }

PVC=$(kubectl get pvc -n "$NAMESPACE" -o jsonpath='{.items[0].metadata.name}')
[[ -n "$PVC" ]] || { echo "No PVC found in namespace $NAMESPACE" >&2; exit 1; }

echo "Backing up co_tracker.db from PVC '$PVC' to $OUT ..."

kubectl run co-tracker-backup --rm -i --restart=Never -n "$NAMESPACE" \
  --image=alpine:3.20 \
  --overrides='{
    "apiVersion": "v1",
    "spec": {
      "containers": [{
        "name": "backup",
        "image": "alpine:3.20",
        "command": ["sh", "-c",
          "apk add --no-cache sqlite >/dev/null 2>&1 && sqlite3 /data/co_tracker.db \".backup '"'"'/tmp/out.db'"'"'\" && cat /tmp/out.db"],
        "volumeMounts": [{"name": "data", "mountPath": "/data", "readOnly": true}]
      }],
      "volumes": [{"name": "data", "persistentVolumeClaim": {"claimName": "'"$PVC"'"}}]
    }
  }' > "$OUT"

if [[ -s "$OUT" ]]; then
    echo "Backup written: $OUT ($(du -h "$OUT" | cut -f1))"
else
    echo "Backup failed — $OUT is empty" >&2
    rm -f "$OUT"
    exit 1
fi
