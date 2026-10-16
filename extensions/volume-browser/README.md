# Volume Browser

A file manager for the EFS volumes attached to a Toft app. It reads the
platform's own declaration of what is mounted, lists what it finds, and offers
the ordinary operations on each one: browse, download, upload, create a folder,
rename, delete.

Nothing is configured by hand. There is no list of paths to keep in step with
the platform — the mounts are discovered, and a mount that is declared but not
present is shown with the reason rather than quietly left out.

Go, standard library only. No database, no external asset, nothing persisted:
the volume is the state.

## What it detects

Toft sets `TOFT_STORAGE` on every isolated workload — a JSON array of
`{name, kind, path, access}`, written by the same code that built the pod's
mounts, so the description and the filesystem cannot disagree. That is the only
source this app uses. It never scans for directories and never guesses a path:
a folder it was not given is not its business.

| What is attached | What you get |
|---|---|
| A **volume** (EFS access point), mounted read-write | Browse, download, upload, new folder, rename, delete |
| A **volume**, mounted read-only | Browse and download. Every write is refused with a sentence, not an error |
| A **store** (S3 through Mountpoint) | Listed, and explicitly not browsable — see below |
| A volume declared but missing from the filesystem | Listed with the reason, which is almost always "attached after this app last started" |

**Object stores are deliberately not browsable.** Mountpoint has no rename, no
append and no real directories; half of the operations on this page would
half-work, and a file manager that silently does the wrong thing on some rows
is worse than one that says which rows it cannot handle. Use the platform's own
store browser for those.

## Requirements

**Isolated mode.** Durable storage is attached below a workload's own boundary,
which a shared process does not have — Toft refuses a shared deploy of an
app with storage mapped. With no storage attached at all, this app starts and
says so on the page rather than looking broken.

**A storage mapping takes effect on the next deployment.** After attaching a
volume in Toft, redeploy or restart this app; nothing arrives on a running
pod.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `BROWSER_TITLE` | no | `Volume browser` | Heading on the page. |
| `MAX_UPLOAD_MB` | no | `1024` | Per-file upload limit. A file over it is refused and nothing partial is left behind. |
| `READ_ONLY` | no | *(off)* | `1` shows every volume read-only regardless of how it is mounted — for an install that wants a viewer beside the app that owns the data. |

`PORT`, `HOST`, `BASE_PATH` and `TOFT_STORAGE` come from the platform.

## Routes

| Path | What |
|---|---|
| `/` | The browser. |
| `/api/volumes` | What is attached, with the reason for anything unusable. |
| `/api/list?vol=&path=` | One directory. |
| `/api/file?vol=&path=` | Download a file. `&inline=1` to view it in the browser. |
| `/api/upload?vol=&path=` | `POST` multipart, field `file`, repeatable. `&overwrite=1` to replace. |
| `/api/mkdir`, `/api/rename`, `/api/delete` | `POST` JSON. Delete needs `recursive` for a folder that is not empty. |
| `/healthz` | Liveness. |

## What it will not do

- **Leave the volume.** Every request names a path relative to a volume root,
  and one function turns that into something the filesystem is asked about. It
  refuses `..`, and it refuses symlinks that point out — the containment check
  runs on a path with every link already resolved. Deleting or renaming a
  symlink acts on the link, never on what it points at.
- **Write to a read-only mount.** The platform's access level is checked before
  the filesystem is touched, so a refusal says "mounted read-only" rather than
  surfacing an `EROFS` from three layers down.
- **Spool an upload through memory or scratch.** Bodies are streamed to the
  volume; a pod's scratch directory is smaller than the volume it is writing to.
- **Undo anything.** This is a filesystem, not a trash can. A delete is a
  delete, and a folder with contents needs the second, explicit consent.

**Who can see it is the platform's job, not this app's.** It shows whatever the
app's mounts contain to whoever can open the app, so leave it private to the
people who should see the data — a Toft extension is admin-installed, but
its visibility is still the app's own setting.

## Running it locally

```bash
mkdir -p /tmp/demo/reports
PORT=8080 HOST=127.0.0.1 \
TOFT_STORAGE='[{"name":"demo","kind":"volume","path":"/tmp/demo","access":"write"}]' \
go run .
```

```bash
go test ./...
```
