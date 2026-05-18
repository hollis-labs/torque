#!/usr/bin/env bash
# Torque DB backup loop.
#
# Runs as a Cerberus-managed os_service resource (id: torque-db-backup).
# Cerberus/launchd keepalive-restarts it if it exits. Each cycle takes a
# consistent online snapshot of the canonical Torque DB via `sqlite3
# .backup` (safe against the live writer), verifies it, gzips it, and
# prunes old snapshots.
#
# Background: on 2026-05-17 the live 15 MB Torque DB was orphaned when a
# repo-local `torque.db` was recreated empty. It was recovered from a
# running process's fds. These backups make that recovery unnecessary
# next time. See the "Torque/Cerberus data-path hardening" task.
#
# Env (all optional):
#   TORQUE_DB_PATH               canonical DB to back up
#   TORQUE_BACKUP_DIR            destination directory
#   TORQUE_BACKUP_INTERVAL       seconds between snapshots (default 3600)
#   TORQUE_BACKUP_HOURLY_KEEP    most-recent snapshots kept regardless of age
#   TORQUE_BACKUP_DAILY_KEEP_DAYS days to keep one-per-day beyond the hourly tier
set -uo pipefail

DB="${TORQUE_DB_PATH:-$HOME/.torque/torque.db}"
DEST="${TORQUE_BACKUP_DIR:-$HOME/dev/backups/torque}"
INTERVAL="${TORQUE_BACKUP_INTERVAL:-3600}"
HOURLY_KEEP="${TORQUE_BACKUP_HOURLY_KEEP:-48}"
DAILY_KEEP_DAYS="${TORQUE_BACKUP_DAILY_KEEP_DAYS:-14}"

log() { echo "$(date -u +%FT%TZ) torque-db-backup: $*"; }

backup_once() {
  mkdir -p "$DEST"
  if [ ! -f "$DB" ]; then
    log "ERROR canonical DB not found: $DB"
    return 1
  fi
  local ts tmp out
  ts=$(date +%Y%m%d-%H%M%S)
  tmp="$DEST/.torque-$ts.partial.db"
  out="$DEST/torque-$ts.db.gz"
  if ! sqlite3 "$DB" ".backup '$tmp'"; then
    log "ERROR .backup failed for $DB"
    rm -f "$tmp" "$tmp"-shm "$tmp"-wal
    return 1
  fi
  if [ "$(sqlite3 "$tmp" 'PRAGMA integrity_check;' 2>/dev/null | head -1)" != "ok" ]; then
    log "ERROR integrity_check failed; discarding snapshot"
    rm -f "$tmp" "$tmp"-shm "$tmp"-wal
    return 1
  fi
  if ! gzip -c "$tmp" > "$out"; then
    log "ERROR gzip failed"
    rm -f "$tmp" "$tmp"-shm "$tmp"-wal "$out"
    return 1
  fi
  rm -f "$tmp" "$tmp"-shm "$tmp"-wal
  log "ok $out ($(wc -c < "$out" | tr -d ' ') bytes gz)"
}

prune() {
  # Tier 1: keep the newest $HOURLY_KEEP snapshots regardless of age.
  # Tier 2: beyond that, keep only the newest snapshot per calendar day,
  #         and drop anything older than $DAILY_KEEP_DAYS days.
  local daycutoff i=0 seen_day="" f fday
  daycutoff=$(date -v-"${DAILY_KEEP_DAYS}"d +%Y%m%d 2>/dev/null) || return 0
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    i=$((i + 1))
    [ "$i" -le "$HOURLY_KEEP" ] && continue
    fday=$(basename "$f" | sed -E 's/^torque-([0-9]{8})-.*/\1/')
    if [ "$fday" -lt "$daycutoff" ]; then
      rm -f "$f" && log "pruned (age): $(basename "$f")"
      continue
    fi
    if [ "$fday" = "$seen_day" ]; then
      rm -f "$f" && log "pruned (daily dedup): $(basename "$f")"
    else
      seen_day="$fday"
    fi
  done < <(ls -1t "$DEST"/torque-*.db.gz 2>/dev/null)
}

log "started: db=$DB dest=$DEST interval=${INTERVAL}s hourly_keep=$HOURLY_KEEP daily_keep_days=$DAILY_KEEP_DAYS"
while true; do
  backup_once || true
  prune || true
  sleep "$INTERVAL"
done
