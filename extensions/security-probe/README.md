# Security Probe

A Toft gallery extension that measures its own isolation. Deploy it like
any other app and open it: it reports, from inside its own per-app account,
exactly what it can and cannot reach — other apps' files, the platform's
secrets, cloud credentials, and the network — with every result checked against
what a correctly isolated app *should* see.

That last part is the point. A tool that only prints "this app can reach
127.0.0.1:3101" tells you nothing; the reader has to already know whether it is
meant to. So each check carries its own expectation, and the verdict is reality
measured against it:

- **pass** — isolation held exactly as the platform intends.
- **accepted** (amber) — a real exposure that is a *documented, accepted trade*
  of shared mode: apps share loopback, the platform API answers on loopback (as
  anonymous), a TCP path to the database exists (but is not authenticable).
  Visible on purpose, not a defect — and the reason sensitive apps run in
  isolated mode.
- **FINDING** (red) — isolation that is supposed to hold did not. Investigate.
- **info** — a fact with no pass/fail (identity, the environment inventory).

## What it checks

| Category | Checks |
|---|---|
| Filesystem | other apps' directories, source, env files and logs; platform-owned files; write confinement |
| Secrets | the platform process's environment (`/proc/1`), other accounts' process environments, this app's own environment |
| Cloud | AWS variables in the environment, the metadata endpoints, and **boto3 credential discovery** against S3 |
| Network | neighbouring app ports, the platform API (and as whom), database reachability |

## Using it

The home page lists every check with its status. **Run** on a row runs one;
**Run every check** runs them all and tallies pass / accepted / findings across
the top. Nothing is persisted and nothing is written outside the app's own
scratch directory — the one write test cleans up after itself.

Set `SECLAB_DB_HOST` to an RDS endpoint to also test whether the app can open a
TCP path to the database. Reachability without the password is
defence-in-depth, not access.

## What a clean run looks like on a hardened install

Shared mode, per-app accounts on: every Filesystem and Secrets check **pass**,
every Cloud check **pass** (no credentials reachable, even where cloud metadata
blocking is off — the app is never handed the credential URI and cannot read
another process's environment), and the Network checks are **accepted** — the
documented shared-mode trades. Zero findings.

It is Flask, standard library plus boto3, no database and no persisted state.
