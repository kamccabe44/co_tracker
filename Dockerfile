# Build stage: pure-Go (no CGO), so the final image can be FROM scratch.
FROM golang:1.24-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/co_tracker . \
    && mkdir -p /out/data

# Final stage: single static binary, ~15 MB image, runs as non-root.
FROM scratch
COPY --from=build /out/co_tracker /co_tracker
COPY --from=build --chown=65532:65532 /out/data /data

ENV LISTEN_ADDR=:8080 \
    DB_PATH=/data/co_tracker.db

USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]

# No shell in this image; point your orchestrator's HTTP health check at /healthz.
ENTRYPOINT ["/co_tracker"]
