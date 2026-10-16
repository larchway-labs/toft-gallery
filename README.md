# Toft Gallery

Every app in Toft's gallery, and the catalog that lists them, in one
repository.

- **The catalog:** [`catalog.json`](catalog.json), served to installs from
  `https://raw.githubusercontent.com/larchway-labs/toft-gallery/main/catalog.json`
- **Extensions:** [`extensions/`](extensions), one folder each
- **Examples:** [`examples/`](examples), one folder each
- **The example kit:** [`kit/`](kit), the shared look every example carries a
  copy of. `kit/vend.sh` copies it into each example that uses it.

Toft's documentation is not a gallery item: it is a public site,
[larchway-labs/toft-docs](https://github.com/larchway-labs/toft-docs), that
every install links to.

## One repository, one folder per app

Each item names the folder it is with `path`:

- An **extension** is cloned from this repository at its tag, and only its
  folder is built — the rest of the repository, `.git` included, never reaches
  the app.
- An **example** is the archive of this repository at its tag, and only its
  folder is unpacked.

So an app here is self-contained: nothing in one folder may reach into another
(a symlink to `../shared` is refused, not followed). That is why every example
carries its own copy of the kit rather than importing it.

**Each app is tagged on its own**, as `<folder>/v<version>` —
`request-desk/v1.1.1`, `flask-hello/v1.2.0`. A change to one app tags that
app and leaves every other app's version alone.

## The catalog is data, never authority

An entry names code and may *ask* for environment variables. It can never grant
a capability, change a platform setting, widen a permission, or exempt an app
from sleep. Everything an extension needs beyond its own code is an
administrator's separate, audited action afterwards.

## Two kinds, and nothing about them is symmetric

| | Extension | Example |
|---|---|---|
| Who sees it | administrators only | anyone who may publish |
| Source | `repo_url` at `ref` — an ordinary git-sourced app | `zip_url`, staged exactly as a human upload |
| Updates | in place, when this file names a newer `version` | never |
| Provenance does | drives the update path | nothing at all |

An example is an ordinary uploaded app from the moment it lands: yours to edit,
never updated from here, and unaffected if the gallery is switched off later.

## What is in it

| Name | Kind | Language | What |
|---|---|---|---|
| Status Board | extension | Go | Poll internal endpoints; show what is up, slow or down. |
| Webhook Inspector | extension | Python (FastAPI) | See exactly what a third party is sending you. |
| Ops Console | extension | JavaScript (Next.js) | On-call, the checklist by cadence, four runbooks. |
| Volume Browser | extension | Go | Browse, upload and manage the EFS volumes attached to an app. |
| Request Desk | extension | Python (FastAPI) | Request and approval on embedded SQLite, driven by the identity Toft supplies. |
| Migration Console | extension | Go | Request, approve and carry out moves of an app from one Toft install to another. |
| Flask Hello | example | Python (Flask) | The smallest complete Flask app, and the base-path trap. |
| CSV Explorer | example | Python (Streamlit) | Upload, filter, chart, download. |
| Next.js Dashboard | example | JavaScript (Next.js) | Stat tiles and a chart with no chart library. |
| Node API | example | JavaScript (Express) | A REST API and the page that calls it. |
| Weekly Report | example | Python (stdlib) | A web app that is only the standard library. |
| MCP Server | example | Python | A Model Context Protocol server, hosted like any other app. |
| Stockroom | example | Go (SQLite) | An inventory register that gets its own report out — PDF, Slack, email, on a schedule. |
| Service Catalog | example | Static | What runs, who owns it, what calls what — one page and one JSON file. |
| Orders | example | Python | A SQLite database read off an attached EFS volume, read-only. |
| Support Review | example | Python (notebook) | A notebook executed once at deploy time and served as the document that fell out. |
| A/B Test | example | R (Shiny) | Two variants in; a difference, an interval and a p-value out. |
| Stats API | example | R (plumber) | R's base statistics behind five HTTP routes. |
| Data Quality Report | example | R (R Markdown) | Point it at a CSV; it renders a page saying what is wrong with the file. |

## Pointing an install at this file

Where the catalog lives is **configuration, not a setting** — no API writes it,
and `PUT /system/settings` naming `gallery_catalog_url` is refused by name.
Either build with this as the compiled-in default
(`config.DefaultGalleryCatalogURL`), or override it per install:

```bash
GALLERY_CATALOG_URL=https://raw.githubusercontent.com/larchway-labs/toft-gallery/main/catalog.json
```

Then set `gallery_deploy_mode` in Admin → Settings to `extensions_only` or
`all`. It defaults to `off`, and off means **no outbound request at all** — a
fresh install does not phone home to a repository nobody asked it to contact.

## Editing this file

- **`id` is a GUID and is the item's identity forever.** Changing it orphans
  every install that took the item; changing the name, the repository or the
  ref does not.
- **`version` must move for an update to be offered**, and it is compared as a
  semver. The version an install records is the one *running* — written when a
  build succeeds, never at enqueue — so a failed update leaves the old number
  because the old code is what is serving.
- **An extension carries `repo_url` + `ref`; an example carries `zip_url`.**
  Carrying both is a validation error and the entry is dropped. Both carry
  `path`, the item's folder; a `path` that is not a plain relative folder
  (`..`, `.git`, absolute) drops the entry.
- **Parsing is lenient per document, strict per entry.** Unknown fields are
  ignored so a newer catalog still works on an older binary, and a bad entry is
  dropped on its own with a counted reason rather than emptying the gallery.
- **Every URL in here must be `https`**, carry no credentials, and not resolve
  into the install's own network. It is re-checked at every redirect hop.
- **`schema_version` is 1.** A document declaring a higher number is a state —
  "this catalog needs a newer Toft" — not a parse failure.
- **An `env` field may be marked `"sensitive": true`.** The deploy dialog then
  collects it as a password and never renders it back. It changes nothing about
  the value — every environment variable is encrypted under the install's own
  key ring either way — and it grants nothing: the catalog *asked*, and what
  the deployer typed is an ordinary app variable an editor can replace.
- **`min_platform_version` is what stops an entry installing onto a build that
  cannot run it.** An install below it renders "needs a newer Toft", which
  is a state the gallery already has, rather than deploying an app that fails
  at its first API call.

## Publishing a new version of an item

1. Commit the change in the item's folder.
2. Tag it `<folder>/v<version>` (`request-desk/v1.2.0`) and push the tag.
   **Never move a tag that has been pushed**: an install that took it has the
   tag's code, and an update is only offered for a newer version.
3. In `catalog.json`, bump the item's `version`, and its `ref` (an extension)
   or the tag in its `zip_url` (an example).
4. Commit and push `main`. Installs pick it up on their next sync (boot, every
   six hours, or an administrator's **Sync now**).

An example is never updated in place, so a new `zip_url` only affects copies
taken from then on. Edit `catalog.json` by hand rather than through a JSON
round trip, which rewrites every `—` in the file.
