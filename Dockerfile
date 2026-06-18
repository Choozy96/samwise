# syntax=docker/dockerfile:1

# ── Build stage ───────────────────────────────────────────────────────────
# Pure-Go build (modernc SQLite needs no cgo), so the binary is static.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/samwise .

# ── Runtime stage ─────────────────────────────────────────────────────────
# Node base so the `claude` CLI (the claude-headless runtime) is available
# inside the container. Harness auth is provided via a mounted volume, not
# baked into the image.
FROM node:22-bookworm-slim AS runtime
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl tini gosu python3 python3-pip \
    && npm install -g @anthropic-ai/claude-code \
    # Python deps for skill scripts (e.g. the migrated calendar/todoist/notion
    # skills). Installed system-wide (--break-system-packages) since the image is
    # a sandbox and skills run with the system python3, not a venv.
    && pip3 install --no-cache-dir --break-system-packages \
        requests python-dotenv tzdata \
        google-api-python-client google-auth google-auth-oauthlib google-auth-httplib2 \
    && ln -sf /usr/bin/python3 /usr/local/bin/python \
    && rm -rf /var/lib/apt/lists/*

# Non-root runtime user; its home holds mounted harness auth (~/.claude). The
# entrypoint starts as root only long enough to fix mount ownership, then drops
# to this user via gosu — so the app and the agent's tools never run as root.
RUN useradd -m -u 10001 app
COPY --from=build /out/samwise /usr/local/bin/samwise
COPY entrypoint.sh /usr/local/bin/entrypoint.sh
RUN sed -i 's/\r$//' /usr/local/bin/entrypoint.sh && chmod +x /usr/local/bin/entrypoint.sh

ENV APP_ENV=prod \
    HTTP_ADDR=:8080 \
    DB_PATH=/data/app.db \
    CLAUDE_BIN=claude
RUN mkdir -p /data && chown app:app /data
VOLUME ["/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD curl -fsS http://localhost:8080/healthz || exit 1

# Runs as root → entrypoint chowns mounts → gosu drops to uid 10001 to exec the app.
ENTRYPOINT ["tini", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["serve"]
