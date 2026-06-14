#!/bin/sh
# Self-heal ownership of the mounted paths, then drop to the non-root app user.
#
# The container runs as app (uid 10001), but ./secrets/claude is bind-mounted to
# /home/app/.claude and any file copied in from the host (scp/docker cp, often as
# root) lands root-owned — then the claude CLI can't write its session state and
# every tool run dies with EACCES. The DB file can have the same problem if copied
# in for a restore. Fixing ownership here on every boot makes it self-correcting.
set -e

UID_APP=10001
GID_APP=10001

chown -R "$UID_APP:$GID_APP" /home/app/.claude 2>/dev/null || true
chown "$UID_APP:$GID_APP" /data 2>/dev/null || true
[ -f /data/app.db ] && chown "$UID_APP:$GID_APP" /data/app.db 2>/dev/null || true

exec gosu "$UID_APP:$GID_APP" samwise "$@"
