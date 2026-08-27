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
    && apt-get install -y --no-install-recommends ca-certificates curl tini gosu python3 python3-pip python3-venv \
    # Install the claude CLI with the NATIVE installer, not npm. The npm package
    # only ships a universal error stub as its bin entry; the real binary lives in
    # a per-platform optionalDependency that npm-in-this-image never installs at
    # all — which is how a stub reached production ("exec format error"). The
    # native installer is architecture-aware and needs no npm. `cp -L` (a copy,
    # never a symlink) is mandatory: /root is 0700, so the per-user agent uids
    # cannot traverse into it. Then VERIFY, so a broken install fails the build
    # rather than production:
    && curl -fsSL https://claude.ai/install.sh | bash \
    && cp -L /root/.local/bin/claude /usr/local/bin/claude \
    && chmod 755 /usr/local/bin/claude \
    && rm -rf /root/.local/share/claude /root/.local/bin/claude /root/.local/state/claude \
    && claude --version \
    # Python deps for skill scripts (e.g. the migrated calendar/todoist/notion
    # skills). Installed system-wide (--break-system-packages) since the image is
    # a sandbox and skills run with the system python3, not a venv.
    && pip3 install --no-cache-dir --break-system-packages \
        requests python-dotenv tzdata \
        openpyxl \
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
    CLAUDE_BIN=claude \
    # Never let claude self-update INSIDE the container: an interrupted or
    # wrong-platform in-place update corrupts its binary ("exec format error")
    # and survives restarts. Updates arrive via image rebuilds only.
    DISABLE_AUTOUPDATER=1
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
