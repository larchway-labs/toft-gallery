# Restoring Postgres from a backup

Sample content. Replace this folder with your own notes.

## Before anything else

Take a fresh backup of the broken database. It is the only copy of the state
that caused the incident, and the restore is about to overwrite it.

## Restoring

```bash
# The nightly dumps land here, one directory per day.
ls /var/backups/postgres/

# Restore into a *new* database first, never over the live one.
createdb app_restore
pg_restore --dbname=app_restore --jobs=4 /var/backups/postgres/2026-08-11/app.dump
```

Check the restored copy before switching anything: row counts on the three
largest tables, and the most recent row in the audit log. A restore that
silently stopped half way through looks exactly like a successful one until
somebody queries the tail.

## Switching over

1. Stop the apps that write to the database.
2. Rename: `app` becomes `app_broken`, `app_restore` becomes `app`.
3. Start the apps and watch the first minute of logs.

## What is lost

Everything written since the dump. Say the window out loud — "we lost writes
between 02:00 and 09:40" — because the people who will find the gap are the ones
who wrote during it.
