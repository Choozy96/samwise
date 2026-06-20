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
# inside the container. Harness auth is provided via a mounted volume, not baked
# into the image.
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

# Runtime user (uid 10001); its home holds mounted harness auth (~/.claude).
# With AGENT_ISOLATION on (default), the orchestrator runs as root so it can drop
# each agent run to an unprivileged per-user uid (base+userID) — strong per-user
# filesystem isolation. With AGENT_ISOLATION=off the entrypoint gosu-drops the
# whole app to this user instead (no per-user isolation, but no root either).
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

# Starts as root → entrypoint fixes mount ownership + credential group, then
# either runs as root (isolation on, dropping each run to a per-user uid) or
# gosu-drops the whole app to uid 10001 (isolation off).
ENTRYPOINT ["tini", "--", "/usr/local/bin/entrypoint.sh"]
CMD ["serve"]
