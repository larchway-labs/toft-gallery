# On-call handbook

Sample content. Replace this folder with your own notes.

## The shift

One primary, one secondary, Monday to Monday. The primary acknowledges; the
secondary is who the primary calls when they need a second pair of eyes, not a
second person waiting to be paged.

## Severities

| Level | Means | Response |
|---|---|---|
| Sev 1 | Customers cannot work | Page immediately, wake people, start a call |
| Sev 2 | Degraded, with a workaround | Acknowledge within 30 minutes |
| Sev 3 | Annoying, not urgent | Ticket it, fix in the next working day |

Choosing a severity is the primary's call and does not need agreement first.
Downgrading later is normal and costs nothing.

## First fifteen minutes

1. Acknowledge, so the page stops and everyone knows it is being looked at.
2. Say in the incident channel what you know and what you do not.
3. Check what changed: recent deploys first, then infrastructure, then load.
4. Stop the bleeding before diagnosing. A rollback or a feature flag now beats
   an explanation in an hour.

## Handover

Write the state at the end of your shift even when nothing happened — "quiet
week, one Sev 3 open on billing exports" is a complete handover. What the next
person cannot use is silence.
