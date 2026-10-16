# Reading an app's logs

Sample content. Replace this folder with your own notes.

## Where they are

Each app's **Logs** tab streams stdout and stderr from the running process. The
stream is live: a crash loop shows as the same startup lines repeating every few
seconds, which is usually faster to recognise than to read.

## Build logs are a different thing

A build log ends when the build does and belongs to one deployment. If the app
never started, the answer is in the build log, not the app log — an app that
failed to install its dependencies has no runtime output to give you.

## Things that look like bugs and are not

- **No output at all.** Python buffers stdout when it is not a terminal. Set
  `PYTHONUNBUFFERED=1` in the app's environment variables.
- **The log stops but the app works.** A process that logs only on error is
  quiet by design. Check the app's own page before assuming it is wedged.
- **Timestamps in the wrong zone.** The platform shows UTC. Your app prints
  whatever it prints.

## When the logs are not enough

Reproduce the request with `curl` against the app's URL, with an API key rather
than a browser session, so the request is exactly the one your client makes and
not a redirect to a sign-in page.
