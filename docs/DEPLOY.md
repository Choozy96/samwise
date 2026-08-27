# Deploy & operate

How to build, run, and maintain the assistant. For end-user help, see
[`user-guide.md`](user-guide.md) (also in the app under **Guide**).

## Prerequisites

- **Go 1.26+** (for building / native dev)
- The **`claude` CLI** authenticated with your Claude subscription
  (`claude` on `PATH`, logged in) — this is what the assistant runs on
- **Docker + Docker Compose** (for the container deployment)

## Secrets (`.env`)

Copy the template and fill it in (never commit `.env`):

```sh
cp .env.example .env
```

Generate the two keys:

```sh
openssl rand -base64 32   # -> MASTER_KEY   (encrypts DB-stored credentials)
openssl rand -base64 32   # -> SESSION_KEY  (signs portal session cookies)
```

| Var | Required | Notes |
|---|---|---|
| `MASTER_KEY` | for stored credentials | 32 bytes base64. Losing it makes encrypted rows unrecoverable. |
| `SESSION_KEY` | in prod | 32+ bytes base64. In dev it's auto-generated if unset. |
| `TELEGRAM_BOT_TOKEN` | optional | From @BotFather; enables the Telegram channel. |
| `ALLOW_AGENT_TOOLS` | optional | `true`/`false`. Enables the scoped runtime tools (Read/Bash/…) so skills can run scripts. Defaults **on in prod**, off for native dev. |
| `IMAGE` | optional | Image compose runs. Defaults to a local build (`samwise:latest`); set to a registry ref to pull a pre-built image (see "Deploy to a VPS"). |
| `DB_PATH`, `HTTP_ADDR`, `LOG_LEVEL`, `APP_ENV` | optional | Sensible defaults; compose overrides `DB_PATH`/`APP_ENV`. |

`.env` is **gitignored**, so every deployment sets up its own. `.env.example` is the
template — keep it in sync with the keys above.

## Run natively (development)

```sh
go run . migrate     # create/upgrade the SQLite schema
go run . serve       # portal on http://localhost:8080
```

Open `http://localhost:8080` — the **first account you create is the admin**.

Useful commands:

```sh
go run . create-user --username alice --password 's3cret!!'   # add a user (first = admin)
go run . set-password --username alice --password 'news3cret!' # reset a password (recovery)
go test ./...                                                  # run tests
```

Users can change their own password in the portal under **Settings → Change
password**. `set-password` is the **headless recovery** path — if the admin is
locked out on a box with no other way in, run it in the container:

```sh
docker compose run --rm orchestrator set-password --username alice --password 'news3cret!'
```

## Run in Docker (deployment)

The container bundles the `claude` CLI. SQLite lives on the `app-data` named
volume — **your data persists across restarts and rebuilds** (only
`docker compose down -v` deletes it).

1. Create `.env` with `MASTER_KEY` and `SESSION_KEY` (see above).
2. Give the in-container `claude` its subscription auth (mount dir is gitignored):

   ```sh
   mkdir -p secrets/claude
   ```

   For **local dev** on your own machine, copying your host credentials is fine:
   `cp ~/.claude/.credentials.json secrets/claude/` (path varies by OS). For a
   **server/VPS, do NOT copy a credential from a machine you actively use with
   Claude** — log the server in independently instead (see "claude auth on a
   headless box" in Troubleshooting). Sharing one credential across two active
   machines makes them rotate each other's OAuth token → recurring `401` on the
   server. (Compose mounts `./secrets/claude` → `/home/app/.claude` read-write so
   the token can refresh.)
3. Build and start — two equivalent ways. With `make` (stamps the real version
   from `git describe`, so the footer and the assistant report e.g. `v0.3.7`):

   ```sh
   make up            # = docker compose up -d --build, version-stamped
   ```

   Or plain compose, no `make` needed — works identically; the version just
   reads `docker`:

   ```sh
   docker compose up --build -d
   ```

   Then either way:

   ```sh
   docker compose logs -f
   curl localhost:8080/healthz      # {"status":"ok"}
   ```

Then open `http://localhost:8080` and create the admin account.

### Multi-user isolation (`AGENT_ISOLATION`)

If more than one person uses the assistant, the agent's host tools (`Bash`,
`Read`, …) must not let one user reach another's data. With `AGENT_ISOLATION`
on — the **default in the Docker image** — the container starts as root and runs
**each agent turn as a distinct unprivileged per-user uid**, so the kernel keeps
each user to their own `/data/workspaces/<id>` and blocks all access to the
database and other users' files. No action needed; it's on by default.

What this means operationally:

- The main process runs as **root inside the container** (it drops each agent run
  to an unprivileged uid). This is contained by the container; don't also run the
  container `--privileged`, and keep the portal off the public internet (below).
- It needs **Linux + root**, which the container provides. On native dev it
  auto-disables with a warning and the app runs as a single unprivileged user.
- To turn it off (e.g. a strictly single-user box where you'd rather the whole
  app stay non-root), set `AGENT_ISOLATION=false`; the entrypoint then drops the
  app to uid 10001 as before. `AGENT_UID_BASE` (default 20000) and
  `AGENT_CRED_GID` (default 10002) rarely need changing.
- The owner's single claude.ai subscription is **shared by all users by design**;
  the agent can read its own auth token. Per-user API keys are the alternative if
  you don't want that — not wired today.

See `SECURITY.md` for the full model and residuals.

## Deploy to a VPS (GCP, etc.)

### Pick an instance (Google Cloud)

Two costs matter: the **build** (a Go compile — `modernc.org/sqlite` alone peaks
~1 GB RAM) and the **runtime** (each chat turn spawns `claude`, a Node process
~200–400 MB, plus any Python skill scripts — so several cron jobs firing at once
can exhaust 1 GB).

| Machine type | vCPU | RAM | Free tier? | Build on it? | Verdict |
|---|---|---|---|---|---|
| `e2-micro` | 2 shared | 1 GB | **Yes** (1/mo, see below) | No — OOMs | **Free single-user.** Build locally, push/pull, add swap. |
| `e2-small` | 2 shared | 2 GB | No (~$13/mo) | Tight — needs swap | Comfortable for one user + cron if you're paying. |
| `e2-medium` | 2 | 4 GB | No (~$25/mo) | Yes | Only for multiple users / heavy concurrent skills. |

**Recommendation:** start on the **`e2-micro` free tier** for a personal,
single-user deploy — build the image on your laptop, push to a registry, pull it
on the VPS, and add swap. Move up to **`e2-small`** only if you hit slowdowns or
OOMs under concurrent jobs. `e2-medium` is overkill for one person.

**Free-tier fine print (verify — Google changes these):** one non-preemptible
`e2-micro` per month, only in **us-west1 (Oregon)**, **us-central1 (Iowa)**, or
**us-east1 (S. Carolina)**; 30 GB-month standard persistent disk; 1 GB/month
network egress free (excludes China & Australia).

**Add swap on micro/small** (the build OOMs without it, and it cushions runtime
spikes):

```sh
sudo fallocate -l 2G /swapfile && sudo chmod 600 /swapfile \
  && sudo mkswap /swapfile && sudo swapon /swapfile \
  && echo '/swapfile none swap sw 0 0' | sudo tee -a /etc/fstab
```

### Prepare the VPS (before you pull or build)

A fresh GCP instance has none of this yet. SSH in
(`gcloud compute ssh INSTANCE`, or `ssh user@VPS_IP`) and set it up once:

1. **Install Docker + the Compose plugin** (Debian/Ubuntu images — GCP's defaults):

   ```sh
   curl -fsSL https://get.docker.com | sudo sh
   sudo usermod -aG docker "$USER"      # run docker without sudo
   newgrp docker                         # apply the group now (or log out/in)
   docker compose version                # verify the plugin is present
   ```

2. **Add swap** (see above) — needed so even a pull-only micro isn't OOM-killed
   under runtime load.

3. **Get the deploy files.** You don't need the whole repo to *run* — just
   `docker-compose.yml` (+ `.env.example`). A shallow clone is easiest:

   ```sh
   git clone --depth 1 https://github.com/Choozy96/samwise.git
   cd samwise
   ```

4. **Create `.env`:**

   ```sh
   cp .env.example .env
   # set MASTER_KEY and SESSION_KEY  (openssl rand -base64 32 each),
   #     IMAGE=<the exact image reference you push below>
   #          e.g. IMAGE=<dockerhub-user>/samwise:latest
   #     optionally TELEGRAM_BOT_TOKEN
   ```

5. **Authenticate the in-container `claude` with its OWN login** (don't share a
   credential with a machine you actively use — see "claude auth on a headless
   box" in Troubleshooting; the short version is `docker exec -it … claude` →
   `/login` → paste the code). You can do this after first boot:

   ```sh
   mkdir -p secrets/claude
   # Quick-but-fragile alternative (only if that machine won't use Claude after):
   #   scp ~/.claude/.credentials.json user@VPS_IP:~/samwise/secrets/claude/
   ```

6. **Authenticate Docker to your registry** so the pull can read your image:

   ```sh
   docker login                                            # Docker Hub
   # or, GCP Artifact Registry (replace <region>):
   gcloud auth configure-docker <region>-docker.pkg.dev    # instance SA needs Artifact Registry Reader
   ```

After this — and once the image is pushed (next section) — you can
`docker compose pull && docker compose up -d --no-build`.

### Build off-box → push → pull (required for micro/small)

Build where there's RAM (your laptop or CI), push to a registry, pull on the VPS.
Only `e2-medium` (4 GB) can reliably build in place with `docker compose up --build`.

Anything in `<angle-brackets>` below is a **placeholder — replace it** (don't type
it literally, or you'll get errors like `dial tcp: lookup REGISTRY: no such host`).
The full image reference is `<registry>/samwise:<tag>`.

**Docker Hub (simplest):** `<registry>` is just your Docker Hub username, `<tag>`
is e.g. `latest`. So the reference is `<dockerhub-user>/samwise:latest`:

With `make` (stamps the real version from `git describe`, so the web footer +
the agent report the right release):

```sh
docker login                                              # Docker Hub user + access token
make push IMAGE=<dockerhub-user>/samwise:latest
# e.g.  make push IMAGE=janedoe/samwise:latest
```

Or plain docker, no `make` — same image, the reported version just reads
`docker` (add `--build-arg VERSION=v0.3.7` yourself if you want it stamped):

```sh
docker login
docker build --platform linux/amd64 -t <dockerhub-user>/samwise:latest .
docker push <dockerhub-user>/samwise:latest
```

> **Build-host architecture matters.** A docker image is **not** architecture-
> neutral — it carries the compiled `samwise` binary and the `claude` CLI, each
> built for one CPU architecture. The VPS is **x86_64**, so images must be
> `linux/amd64`. On an x86_64 build host (Windows/WSL, Intel Mac, Linux PC)
> that is the default and you can ignore this. On an **Apple Silicon Mac** the
> default is arm64, and pushing that image breaks production with the very
> `exec format error` documented below — so pass `--platform linux/amd64`.
> `make image` / `make push` already pin it (`PLATFORM ?= linux/amd64`); the
> cross-build runs under emulation, so expect it to be slower than a native one.

Then on the VPS — set `IMAGE` in `.env` to the SAME reference:

```sh
# IMAGE=<dockerhub-user>/samwise:latest in .env, then:
docker login                                              # if the repo is private
docker compose pull
docker compose up -d --no-build
```

**GCP Artifact Registry:** `<registry>` is
`<region>-docker.pkg.dev/<project-id>/<repo>` — so the full reference looks like
`us-central1-docker.pkg.dev/my-proj/apps/samwise:latest`:

```sh
# one-time: create the repo + let Docker auth to it
gcloud artifacts repositories create <repo> --repository-format=docker --location=<region>
gcloud auth configure-docker <region>-docker.pkg.dev

make push IMAGE=<region>-docker.pkg.dev/<project-id>/<repo>/samwise:latest
# or without make:
#   docker build --platform linux/amd64 -t <region>-docker.pkg.dev/<project-id>/<repo>/samwise:latest .
#   docker push  <region>-docker.pkg.dev/<project-id>/<repo>/samwise:latest
```

On the VPS, set `IMAGE` to the same reference; the instance's service account
needs the **Artifact Registry Reader** role to pull.

### How the volume works (build local, run on VPS)

Building the image and storing your data are **completely separate things** — this
is the key to why building locally then running on the VPS just works:

- **The image** (what you build locally and push) holds only the compiled app +
  the `claude` CLI + Python. It is **stateless** — no database, no credentials, no
  settings travel inside it.
- **Your data** lives in the Docker **named volume** `samwise_app-data`, which
  is created **on the VPS's own disk** the first time you run `docker compose up`
  there. The SQLite DB (`/data/app.db`) is created inside it by the first-boot
  migration, and it survives image pulls, updates, and `down`/`up` — only
  `docker compose down -v` erases it.

So *where you build has zero effect on the volume.* The volume always lives on
whichever host **runs** the container. Build locally → push → pull → run on the
VPS, and the VPS spins up a fresh empty volume + DB on first boot; you then create
the admin account through the portal as usual.

Three things are **not** in the image and must exist on the **VPS filesystem**
(they're bind-mounted and read at runtime):

1. **`.env`** — create it on the VPS (`cp .env.example .env`, fill the keys).
2. **`./secrets/claude/.credentials.json`** — give the in-container `claude` its
   own login (`docker exec -it … claude` → `/login`, paste the code; see
   Troubleshooting). Don't share a credential with a machine you actively use with
   Claude — they'd rotate each other's token and 401 the server.
3. **`./nginx/`** (the `templates/` config + `init-letsencrypt.sh`) — only if
   you enable the TLS proxy.

You therefore don't need the full repo on the VPS to *build* — just
`docker-compose.yml` plus those mounted files. A shallow `git clone` (or even
`scp`-ing the compose file and creating the three files by hand) is enough.

**Bringing existing local data along:** if you've already been running locally and
want that DB on the VPS, copy your local `app.db` into the VPS volume *after* first
boot creates it — same `chown 10001:10001` step as **Backups & restore** below.
Otherwise just start fresh on the VPS.

### Don't expose the portal raw to the internet

The portal is **plain HTTP** with logins + personal data. Compose binds it to
`127.0.0.1:8080` by default (not reachable from outside). Choose one:

- **SSH tunnel (simplest for one person — recommended):** leave 8080 bound to
  localhost and open **no** web port at all. From your laptop, forward the port
  over your existing SSH connection and browse it locally:

  ```sh
  ssh -L 8080:localhost:8080 user@VPS_IP        # then open http://localhost:8080 on your laptop
  # GCP equivalent (use --ssh-flag; the bare "-- -L" form breaks on some shells):
  gcloud compute ssh INSTANCE_NAME --zone=ZONE --ssh-flag="-L 8080:localhost:8080"
  ```

  The portal is reachable only while that SSH session is open, tunnelled through
  SSH's encryption — no domain, no TLS cert, nothing exposed beyond port 22. (This
  is SSH *local* forwarding. A true **reverse** tunnel — `ssh -R` initiated *from*
  the VPS — is only needed if the VPS itself can't accept inbound connections,
  e.g. it's behind NAT; a GCP VM with a public IP doesn't need it.)
- **TLS reverse proxy (HTTPS via Let's Encrypt — recommended for 2+ users):**
  the bundled **nginx + certbot** services terminate TLS and auto-renew the
  certificate. It's an opt-in compose profile, so it only runs when you ask.
  Steps:

  1. **Get a domain** and add a DNS **A record** → your VPS's public IP. (No
     domain? A free `*.duckdns.org` works.) Let's Encrypt won't issue for a bare
     IP, so a hostname is required.
  2. In `.env`:

     ```sh
     SITE_ADDRESS=samwise.example.com   # your domain
     CERTBOT_EMAIL=you@example.com      # renewal notices (optional)
     TRUST_PROXY=true                   # read real client IP from the proxy
     ```
  3. **Firewall:** open **80 + 443 only** (80 is needed for the ACME challenge
     and the HTTP→HTTPS redirect); keep **8080 closed** — it stays loopback-only
     and nginx reaches the app over the internal network.

     ```sh
     gcloud compute firewall-rules create samwise-web \
       --allow=tcp:80,tcp:443 --target-tags=samwise --direction=INGRESS
     # (tag your instance with `samwise`, or use your own targeting)
     ```
  4. **Get the first certificate** (one-time; handles the nginx/cert
     chicken-and-egg by issuing a temporary self-signed cert, then the real one):

     ```sh
     sh nginx/init-letsencrypt.sh        # uses SITE_ADDRESS/CERTBOT_EMAIL from .env
     ```

     Tip: set `CERTBOT_STAGING=1` in `.env` for a dry run against Let's Encrypt's
     staging CA first (its rate limits are strict), then unset it and re-run.
  5. Bring it up:

     ```sh
     docker compose --profile tls up -d
     docker compose logs -f nginx certbot
     ```

  Then browse `https://samwise.example.com`. The cert lives in the
  `certbot-conf` volume; the `certbot` container renews it and nginx reloads
  every 6h to pick it up. (The default `docker compose up`, without
  `--profile tls`, is unchanged — no proxy, portal stays private.)
- **Tailscale:** join the VPS to your tailnet and reach `http://<tailscale-ip>:8080`
  privately (change the port binding to the tailscale interface).
- **Firewall to your IP:** restrict 8080 to your home IP (least good — still plain
  HTTP).

### `exec format error` — claude isn't a valid executable

`headless: start claude: fork/exec /usr/local/bin/claude: exec format error`
means the claude executable inside the container isn't a real binary. Check
what it actually is:

```sh
docker exec samwise-orchestrator-1 sh -c \
  'f=$(readlink -f /usr/local/bin/claude); ls -la "$f"; head -c 16 "$f" | od -c | head -2'
```

Three known causes:

- **Broken at BUILD time** (file dated the image build; a few hundred bytes;
  starts with `echo "Error:` — a shebang-less stub): the `@anthropic-ai/claude-code`
  npm package ships a **universal error stub** as its `bin` entry; the real
  executable comes from a per-platform **optionalDependency**
  (`@anthropic-ai/claude-code-linux-x64` and friends) wired up by its
  postinstall. In the `node:22-bookworm-slim` base, npm **never installs that
  optionalDependency at all** — the package's nested `node_modules` comes out
  empty, and neither `--include=optional` nor running the postinstall by hand
  changes that (there is nothing for it to find). So **no npm reinstall inside
  the container recovers**. Hot-patch the running container with the native
  installer, then copy the binary somewhere the per-user agent uids can read
  (`/root` is 0700 — a symlink into it breaks isolated runs):

  ```sh
  docker exec samwise-orchestrator-1 sh -c \
    'curl -fsSL https://claude.ai/install.sh | bash \
     && rm -f /usr/local/bin/claude \
     && cp -L /root/.local/bin/claude /usr/local/bin/claude \
     && chmod 755 /usr/local/bin/claude && claude --version'
  ```

  Then **rebuild the image**. The Dockerfile no longer uses npm for the CLI at
  all: it runs the same native installer, `cp -L`s the binary to
  `/usr/local/bin/claude`, and verifies `claude --version` at build time, so a
  broken install fails the build loudly instead of shipping a stub.
- **Corrupted at RUNTIME** (it worked for days; the file is newer than the
  image): claude's auto-updater replaced its own binary in-place and the
  download was interrupted (an OOM kill mid-update does this) or fetched the
  wrong platform. `docker compose up -d --force-recreate orchestrator` restores
  the image's install. Images now set `DISABLE_AUTOUPDATER=1` so this can't
  recur — CLI updates arrive via image rebuilds.
- **WRONG-ARCH image** (nothing is a stub — every binary is fine, just built
  for the wrong CPU; `samwise` itself usually fails too, not only `claude`):
  the image was built on an arm64 host (an Apple Silicon Mac) and the VPS is
  x86_64. Confirm by comparing the two:

  ```sh
  docker image inspect $IMAGE --format '{{.Os}}/{{.Architecture}}'   # want linux/amd64
  uname -m                                                           # on the VPS: x86_64
  ```

  Rebuild with `--platform linux/amd64` (what `make image` / `make push` do)
  and push again. See the build-host note under **Build off-box → push → pull**
  above.

### claude auth on a headless box — give the VPS its OWN login

> **Do NOT just copy a `.credentials.json` from a machine you actively use with
> Claude.** claude.ai's OAuth **rotates the refresh token on every refresh**, so
> two machines sharing one credential keep invalidating each other — the busy
> machine (e.g. your laptop running Claude Code) refreshes, the rotated-out token
> on the VPS goes dead, and every run on the VPS fails with
> `API Error: 401 Invalid authentication credentials`. A copied credential works
> only until the source machine next refreshes.

**Give the VPS its own independent login instead.** Claude Code can authenticate
on a headless box — the CLI does a paste-the-code flow: it prints a URL, you open
it in a browser on **any** machine, approve, and paste the code back. Run it so it
writes into the mounted credential dir:

```bash
# container name = <compose project dir>-orchestrator-1 — check `docker ps`
docker exec -it samwise-orchestrator-1 \
  env HOME=/home/app CLAUDE_CONFIG_DIR=/home/app/.claude claude
#   then run  /login  and follow the URL + paste-the-code prompt
docker compose restart orchestrator
```

This gives the server its **own** OAuth session (separate refresh-token chain), so
nothing rotates its token out from under it. Best practice: treat this as a
**server-dedicated login you don't use interactively elsewhere**, so the VPS's own
runs are the only thing refreshing it.

If a second login revokes your laptop's session (some accounts allow several
device sessions, some don't — check), use a **separate account dedicated to the
server**. Copying a credential up from a logged-in machine still works as a quick
fix, but only if that machine won't be used with Claude afterward.

#### Expect to re-authenticate every ~30 days (confirmed session limit)

Even a correctly set-up, server-dedicated login **expires roughly every 30
days** and has to be re-created with `/login`. The tell-tale error is:

```
Failed to authenticate: OAuth session expired and could not be refreshed
```

The OAuth session / refresh token has a hard lifetime of roughly **30 days**,
independent of how often it's used. Two consecutive intervals now confirm it:

| Date | Event |
|---|---|
| 2026-06-23 | VPS re-authenticated with its own `/login` |
| 2026-07-23 | All runs failed with `OAuth session expired and could not be refreshed`; re-authenticated same day |
| 2026-08-23 | Expired again — 31 days of otherwise-healthy operation, no config change, no credential sharing |

Treat it as routine maintenance, not an incident: when the error above appears,
re-run the `/login` flow at the top of this section and restart the
orchestrator. Expect the next expiry ~30 days after each re-auth (from
2026-08-23: around 2026-09-22).

**Don't confuse the two auth failures** — they have different causes and fixes:

| Error | Meaning | Fix |
|---|---|---|
| `OAuth session expired and could not be refreshed` | The refresh token itself is dead — session reached end of life | Re-run `/login` (above), then restart |
| `401 Invalid authentication credentials` | The token was rotated out from under this machine — usually a credential **shared** with another active machine | Give the server its **own** login; stop sharing |

### Credentials dir ownership (`EACCES … /home/app/.claude`) — self-healed

The container runs as the non-root user **`app` (uid 10001)**, and `claude` writes
runtime state — `session-env/`, `sessions/`, `projects/` — into its home,
`/home/app/.claude`, which is the **bind-mounted `./secrets/claude`**. A bind mount
keeps the **host** directory's ownership, so a file created or `scp`-ed there as
`root` would otherwise be unwritable by uid 10001 and **every tool run** would fail
with `EACCES: permission denied, mkdir '/home/app/.claude/session-env'`.

**The image now fixes this automatically**: the container's entrypoint starts as
root, `chown`s `/home/app/.claude` (and the DB) to uid 10001, then drops to the
`app` user via `gosu` before running the app. So you can copy credentials in as any
user and just `docker compose restart` — ownership self-corrects on boot. No manual
`chown` needed.

If you're on an **older image** (before this change) and hit the EACCES error, the
manual fix still works:

```sh
sudo chown -R 10001:10001 secrets/claude      # or your $CLAUDE_CONFIG_DIR
docker compose restart
```

**This is the only mount that needs it.** `./secrets/claude` (→ `/home/app/.claude`)
is the sole host folder `claude` reads/writes. The app's data — `/data`, including
the per-user workspace where Bash/Python actually run (`/data/workspaces/<id>`) — is
a **named volume** seeded from the image (where `/data` is already `chown app`), so
Docker owns it as uid 10001 automatically. You'd only chown inside `/data` if you
**manually copy** a file into the volume (e.g. restoring a DB — see Backups below).

### Telegram: one poller only

The same bot token can't be long-polled by two instances (Telegram returns 409).
Stop any local instance, or use a separate bot for the VPS.

## Update

```sh
git pull
docker compose up --build -d   # rebuilds; migrations apply on start; data persists
```

## Backups & restore

- A nightly maintenance job snapshots the DB (`VACUUM INTO`) inside the volume.
- To copy the live DB out of the running container:

  ```sh
  docker compose cp orchestrator:/data/app.db ./backup-app.db
  ```

- To restore into a volume, stop the app, copy the file in, and ensure it's owned
  by the container user (`uid 10001`):

  ```sh
  docker compose stop
  docker run --rm -v samwise_app-data:/data -v "$PWD":/src alpine \
    sh -c "cp /src/backup-app.db /data/app.db && chown 10001:10001 /data/app.db"
  docker compose start
  ```

## Notes

- **Single-owner, ≤5 trusted users** on the owner's Claude subscription. Verify
  the current Anthropic terms for subscription-backed programmatic use before
  relying on it (see the README ToS note).
- `npx`-based MCP servers download on first use — pre-install them so they connect
  within the startup timeout.
- Egress from the container is open by default (agents fetch the web); restrict it
  in `docker-compose.yml` if needed.
