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
2. Give the in-container `claude` your subscription auth — copy your host
   credentials into the gitignored mount dir:

   ```sh
   mkdir -p secrets/claude
   cp ~/.claude/.credentials.json secrets/claude/      # path varies by OS
   ```

   (compose mounts `./secrets/claude` → `/home/app/.claude`, read-write so the
   OAuth token can refresh.)
3. Build and start:

   ```sh
   docker compose up --build -d
   docker compose logs -f
   curl localhost:8080/healthz      # {"status":"ok"}
   ```

Then open `http://localhost:8080` and create the admin account.

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
   git clone --depth 1 https://github.com/YOUR-ORG/samwise.git
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

5. **Place the claude credentials** so the in-container `claude` is authenticated
   (the box is headless — you can't `claude login` there):

   ```sh
   mkdir -p secrets/claude
   # from your laptop:
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

```sh
# Local / CI — log in once, then build + push:
docker login                                              # Docker Hub user + access token
docker build -t <dockerhub-user>/samwise:latest .
docker push <dockerhub-user>/samwise:latest
# e.g.  docker build -t janedoe/samwise:latest .

# On the VPS — set IMAGE in .env to the SAME reference, then:
#   IMAGE=<dockerhub-user>/samwise:latest
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

docker build -t <region>-docker.pkg.dev/<project-id>/<repo>/samwise:latest .
docker push  <region>-docker.pkg.dev/<project-id>/<repo>/samwise:latest
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
2. **`./secrets/claude/.credentials.json`** — copy it up from a machine where
   you're logged into `claude` (the VPS is headless; you can't `claude login` there).
3. **`./Caddyfile`** — only if you enable the TLS proxy.

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
- **TLS reverse proxy:** for always-on access from any device. Uncomment the
  `caddy` service in `docker-compose.yml`, point a domain at the VPS, create a
  `Caddyfile`, and open the firewall for **80 + 443 only** (keep 8080 closed).
  Caddy auto-provisions a cert.
- **Tailscale:** join the VPS to your tailnet and reach `http://<tailscale-ip>:8080`
  privately (change the port binding to the tailscale interface).
- **Firewall to your IP:** restrict 8080 to your home IP (least good — still plain
  HTTP).

### claude auth on a headless box

The OAuth token in `./secrets/claude/.credentials.json` refreshes automatically
(the mount is read-write). But if it fully expires, you can't run an interactive
`claude` login on a headless VPS — copy a fresh `.credentials.json` up from a
machine where you're logged in.

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
