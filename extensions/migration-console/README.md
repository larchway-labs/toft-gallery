# Migration console

A change-request workflow for moving an app from one Toft install to
another — from the one your developers use to the one your customers use.

A developer opens a request to move one of their apps, attaches whatever the
change board needs to see, and submits it. A named reviewer reads the request,
looks at the attachments, and approves or rejects. An approval is followed —
straight away, or at a time somebody scheduled — by a push that either creates
the app on the target install or ships a new version of one that is already
there.

This is a Toft Gallery extension. An administrator installs it from the
Gallery on the **source** install, then completes a short setup.

## What it does

- **Requests pin a release, not an app.** The reviewer approves a specific
  build. If the app is deployed again before the migration runs, the console
  says so and still ships what was approved.
- **The submitter never picks the destination.** The first successful deploy
  binds a source app to a target app, and every later request follows that link.
- **The approver chooses the name and the audience.** What the app is called on
  the far side, and whether it is private, signed-in only, or public. The choice
  carries to the next migration of the same pair.
- **Notes and documents in two places.** A standing note and standing documents
  on the app, which every request for it inherits; and a note and documents on
  each request, for that release.
- **The requester can add other people to a request.** They can read it, watch
  it, and raise the next migration request for the same app.
- **An approved migration can be called off.** Until the push starts, a reviewer
  or the person who raised it can cancel it, with a reason. *Cancelled* is a
  state of its own, not a wording: withdrawn is before anybody decided,
  rejected is a reviewer saying no, and cancelled is a change that was approved
  and then stopped. A year later those are three different answers.
- **Anything that did not go can be raised again in one click.** A failed,
  rejected, withdrawn or cancelled request offers a link that opens a new one
  with the app, the release, the target, the destination and the reason already
  filled in. It is a prefill and never a reopening: the new request gets its own
  review, because the old approval was for those bytes.
- **Everything is recorded.** Every action — submitted, approved, rejected,
  scheduled, pushed, failed, attached, edited — is a row, and the record is
  readable per app: *what has ever shipped from this app, and where is it now*.
- **It can email.** The requester when their migration finishes, and an
  administrators' group when something fails. Both need an email connection
  attached to this app; without one, nothing about email appears at all.
- **Security scan results have a place.** SAST and DAST columns exist and
  nothing fills them yet. Toft's own dependency verdict for the exact
  release being promoted is shown, because the platform already produces it.

## Installing it

Install from the Gallery. The deploy dialog asks for two values:

| | |
|---|---|
| `DATABASE_URL` | A Postgres database for the console's own records. Use one you already back up — this is an audit record of production deploys, not a cache. |
| `TARGET_KEY_SECRET` | Any long passphrase. Each target's deploy key is encrypted with it before it is written to the database. |

Then five steps, each an ordinary administrator action:

1. **Turn on viewer identity** for this app, on the app's settings in Toft.
   Without it the console cannot tell who raised or approved anything.
2. **Set its visibility to signed-in viewers.** An extension lands private,
   which is its owner and this install's administrators; everybody who raises or
   reviews a migration has to be able to open it. Nothing becomes public — the
   platform still decides who is signed in, and this app still refuses anybody
   its own roster does not name.
3. **Add this install as the source.** Mint a deploy key here with the
   **read** scope and paste it in on the Targets page. Read-scoped on purpose:
   the credential the console holds on the install it runs on can pull a release
   and cannot deploy anything.
4. **Add a target.** An administrator on the other install mints a
   **deploy**-scoped key there and gives it to you. No credential moves between
   installs on its own.
5. **Add a scheduled task** pointing at `/tasks/tick`. It runs due migrations,
   watches builds on the far side, and re-tests every target once a day. It is
   also what keeps this app awake. Run now works too — a schedule fired by hand
   carries your role rather than the platform's, and the console accepts an
   owner or editor of this app so you can test it.

Optionally, **attach an email connection** to this app if you want
notifications.

The Install page inside the app lists all of this and shows what is still
outstanding.

## What it does not do

- It does not enforce an order between environments. A target is a target;
  nothing checks that dev comes before UAT.
- It does not avoid a rebuild. Toft exports source and imports source, so
  the target builds what it receives.
- It does not copy environment variable **values**, ever. It seeds the names
  once, when it creates the app, and never touches them again.
- It does not run a security scanner. It has a place to record one.
- It does not have a multi-step approval chain. One approval, by one reviewer
  who did not raise the request.
- It does not retry. A push that failed is not re-run and a cancelled request is
  not restarted — both offer to raise the same request again, which goes back
  through review.
- It does not recall a push that has started. Once the bundle is on the target,
  what happens next is decided on that install.
- It cannot list your users. The key it holds may read group names, counts and
  addresses and never who is in a group, so where it asks for a person it
  suggests the people it already has a name for — anyone who has raised, decided
  or been named on a request here — and accepts any Toft user id.

## Things worth knowing

**The console holds keys for your estate.** Each target's key can deploy to any
app on that install. If the console is compromised, so are those keys. The
controls that exist are real — a deploy key cannot delete an app, change who may
do what, or read a credential, and any of them can be revoked from that
install's admin page in one click — but "the console only deploys what was
approved" is app code, not a boundary.

**Documents are stored in the database and served back carefully.** A PDF
renders in the page; everything else downloads. The console decides how to serve
a file from its own short list and never from what the uploader called it.

**Two people are never the same person.** A reviewer cannot approve their own
request. The one exception is the optional per-target "send newest" button on an
established link, which needs a reason and is flagged in the record. That button
lives on the Links page, which is why the page admits a reviewer as well as an
editor: the roster is deliberately not the administrators, so a page only
editors could open would hide the control from everyone entitled to press it.
Deleting a link is still an editor's.

**A first migration to a target needs a group on that target.** A deploy key
belongs to nobody, so an app the console creates has to be owned by a group over
there — and the approval screen reads that install's group directory and offers
it as a list, refusing a group that has nobody in it. A brand-new Toft is
on the demo tier and the demo tier locks Groups, so a fresh target has none at
all; the console says so at the approval rather than letting it arrive as a push
failure on the far side several minutes later.

## Building it

```bash
go build ./...
go test ./...
```

Go, standard library plus `pgx`. No Node, no bundler, no CDN: the pages are Go
templates and the interactions are htmx, which is vendored into the repository
and served from the app's own path. Every dependency must be pure Go — Toft
builds Go apps in an image with no C compiler, and a package that needs one
fails with an error that does not explain itself.

The stylesheet copies Toft's tokens rather than importing them, because a Go
app cannot import a React component or a Tailwind build. Every value in it was
read out of the running product rather than picked off a palette, and the two
typefaces — Geist and Geist Mono, the same faces the platform ships — are served
from this app's own `/static` so an install with no egress renders the way every
other install does. When Toft retunes a token, this app does not find out;
that is the known cost of the extension shape, and it is why the file says where
each number came from.
