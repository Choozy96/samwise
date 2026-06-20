#!/bin/sh
# Self-heal ownership of mounted paths, then start the app.
#
# ./secrets/claude is bind-mounted to /home/app/.claude and a DB copied in for a
# restore can land root-owned; fixing ownership here on every boot keeps the
# claude CLI able to write its session state and the app able to open the DB.
set -e

UID_APP=10001
GID_APP=10001
CRED_GID="${AGENT_CRED_GID:-10002}"
APP_HOME=/home/app

# Point the claude CLI at the mounted config dir explicitly (rather than relying
# on $HOME/.claude), so all of its state — credentials, session/project files —
# lives in one shared, group-accessible place regardless of which uid runs it.
export HOME="$APP_HOME"
export CLAUDE_CONFIG_DIR="$APP_HOME/.claude"

ISO="$(printf '%s' "${AGENT_ISOLATION:-}" | tr '[:upper:]' '[:lower:]')"
case "$ISO" in
0 | false | off | no)
	# Isolation explicitly off: keep the tighter posture of running the whole
	# app (and the agent, which shares this uid) as the unprivileged app user.
	chown -R "$UID_APP:$GID_APP" "$APP_HOME/.claude" 2>/dev/null || true
	chown "$UID_APP:$GID_APP" /data 2>/dev/null || true
	[ -f /data/app.db ] && chown "$UID_APP:$GID_APP" /data/app.db 2>/dev/null || true
	exec gosu "$UID_APP:$GID_APP" samwise "$@"
	;;
esac

# Isolation on (default): the orchestrator runs as root so it can drop each agent
# run to an unprivileged per-user uid (base+userID). It locks down /data, and at
# run time gives each uid its OWN claude config dir inside that user's 0700
# workspace — so claude's transcripts/session state are private per user.
#
# Only the claude.ai CREDENTIAL is shared (one subscription, by design): the
# per-user config dir symlinks it from here. Keep this dir group-readable and
# -writable (via a dedicated numeric gid carried as each run's supplementary
# group) so a run uid can authenticate and refresh the token through that
# symlink, while still being unable to read the root-owned DB.
chmod 0711 "$APP_HOME" 2>/dev/null || true
# The previous design wrote transcripts/state into this shared, group-readable
# dir. Remove that leftover so it can't be read cross-user; per-run state now
# lives in each user's private workspace.
rm -f "$APP_HOME/.claude.json" 2>/dev/null || true
for d in projects todos shell-snapshots statsig history.jsonl .claude.json; do
	rm -rf "$APP_HOME/.claude/$d" 2>/dev/null || true
done
chown -R "$UID_APP:$CRED_GID" "$APP_HOME/.claude" 2>/dev/null || true
chmod -R u+rwX,g+rwX "$APP_HOME/.claude" 2>/dev/null || true
find "$APP_HOME/.claude" -type d -exec chmod g+s {} + 2>/dev/null || true

exec samwise "$@"
