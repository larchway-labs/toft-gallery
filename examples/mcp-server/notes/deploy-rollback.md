# Rolling back a bad deploy

Sample content. Replace this folder with your own notes — the tools do not care
what is in them.

## When to roll back rather than fix forward

Roll back if the failure is user-visible and the fix is not already written and
reviewed. A rollback takes a minute and is reversible; a hurried fix is neither.

## Steps

1. Open the app in Toft and go to the **Deployments** tab.
2. Find the last deployment marked *succeeded* before the current one.
3. Press **Rollback**. The previous release directory is still on disk, so
   nothing is rebuilt and no dependency is re-fetched.
4. Watch the app's status return to *running*, then check the page yourself.

## After a rollback

- The app is now serving code that does not match the default branch. Auto-deploy
  is still on, so the next push deploys again — hold the branch until the fix is
  ready.
- Say in the team channel which release is live. A rollback nobody announced is
  found by the next person to wonder why their change is missing.

## What a rollback does not fix

A bad database migration. Rolling back the code leaves the schema where the
failed release put it. If the deploy included a migration, treat the schema as
the first problem and the code as the second.
