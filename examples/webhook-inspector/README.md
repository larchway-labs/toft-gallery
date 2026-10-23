# Webhook Inspector

See what a third party is actually sending you. Point any webhook at this app's
`/hook` path and every request is captured — method, path, query string,
headers and body — and shown newest first.

FastAPI. No database, no external assets.

## Nothing is persisted

The buffer lives in the process and is lost on restart. That is deliberate:
captured webhooks routinely carry signatures, customer identifiers and one-time
tokens, and a debugging aid that quietly accumulates them on disk is a
liability nobody remembers creating. `Authorization`, `Cookie` and `X-API-Key`
are recorded as `<redacted>` — present, without the value, because "did they
send one at all" is usually the real question.

## Configuration

| Variable | Required | Default | Meaning |
|---|---|---|---|
| `MAX_REQUESTS` | no | `50` | How many requests to keep (1–500). |
| `WEBHOOK_TOKEN` | no | — | A shared secret. When set, a capture must carry `X-Webhook-Token` or `?token=`. |

`WEBHOOK_TOKEN` is a convenience, not a boundary: this app already sits behind
Toft's authorization, and the token exists so the URL can be handed to an
external sender without it becoming an open endpoint for everyone else who can
reach the app.

## Routes

| Path | What |
|---|---|
| `/` | The captured requests. |
| `/hook`, `/hook/{anything}` | Capture. `GET`, `POST`, `PUT`, `PATCH`, `DELETE`. |
| `/api/requests` | The same data as JSON. `DELETE` clears it. |
| `/healthz` | Liveness. |

## Trying it

```bash
curl -X POST https://toft.example/apps/webhook-inspector/hook \
     -H 'content-type: application/json' \
     -d '{"event":"invoice.paid","amount":4200}'
```

Locally:

```bash
pip install -r requirements.txt && python main.py
```
