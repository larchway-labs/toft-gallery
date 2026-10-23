"""The other entrypoint.

`toft.toml` declares this as the `close` job, and the platform runs it as
an isolated workload: its own container, its own memory limit, its own deadline,
and a durable record whether it succeeds or not. Same repository, same release,
same storage — a different command.

Run it by hand too. `python worker.py 2026-06` works from a checkout, which is
the property that makes a job testable at all.
"""

from __future__ import annotations

import datetime
import sys

import engine
import ledger
import platform_seam as seam

try:
    import toft
except Exception:                                  # pragma: no cover
    toft = None                                      # type: ignore[assignment]


def which_month(run, argv: list[str], fallback: str) -> tuple[str, str]:
    """The month this close is for, and the sentence saying how it was chosen.

    A person names one (``--param period=``, or the argument from a checkout).
    A schedule names an *instant* — the tick it fired for, never "now" — and a
    month-end close fired on the 1st is for the month before it. The seeded
    register only holds three months, so a tick past them falls back to the
    settings document and the log says so rather than closing an empty month.
    """
    params = run.params if run else {}
    if params.get("period"):
        return params["period"], "the period it was started with"
    if len(argv) > 1:
        return argv[1], "the period on the command line"
    when = run.scheduled_for if run else None
    if when is not None:
        prev = (when.replace(day=1) - datetime.timedelta(days=1)).strftime("%Y-%m")
        if prev in ledger.PERIODS:
            return prev, f"the month before {when:%Y-%m-%d %H:%M %Z}".rstrip()
        return fallback, (f"scheduled for {when:%Y-%m-%d}, but {prev} is not in the seeded "
                          f"register, so the settings document's period")
    return fallback, "the settings document's period"


def main(argv: list[str]) -> int:
    # `toft.run` is the job's own view of itself, and it is None everywhere else.
    # That is how one module shared between two entrypoints tells which one it
    # is in, without a flag anybody has to remember to pass.
    run = getattr(toft, "run", None) if toft else None
    cfg, _version, _note = seam.settings()
    period, why = which_month(run, argv, cfg["period"])
    ledger.init()

    def progress(pct: int, msg: str) -> None:
        # Two channels and they are not the same thing: stdout is the run's log,
        # which a person reads; `progress` is the number the platform draws.
        print(f"[{pct:3d}%] {msg}", flush=True)
        if run:
            try:
                run.progress(pct)
            except Exception as exc:              # never fail the work over telemetry
                print(f"       (progress not recorded: {exc})", flush=True)

    print(f"Closing {period} — {why}", flush=True)
    if run and run.rerun_of:
        # A re-run carries the original's params and its scheduled_for, so it
        # closes the same month; this line is how the log says it is a repeat.
        print(f"  re-run of   {run.rerun_of[:8]}", flush=True)
    print(f"  tolerance   {cfg['tolerance_pence']}p", flush=True)
    print(f"  date window {cfg['date_window_days']} days", flush=True)
    print(f"  register    {ledger.where().path}  ({ledger.where().source})", flush=True)

    result = engine.run_close(
        period,
        tolerance_pence=int(cfg["tolerance_pence"]),
        date_window_days=int(cfg["date_window_days"]),
        trigger="schedule" if run and run.started_by == "schedule" else "job",
        job_run_id=(run.run_id if run else ""),
        progress=progress,
    )

    line = engine.summary_text(result)
    print(line, flush=True)

    # The one-line summary rides on the run's progress message, which is what
    # the page lists; the six numbers are the run's output.
    if run:
        run.progress(100, message=line)
        run.write_output_json(result)

    if cfg.get("announce"):
        print(seam.announce(line, f"Reconciliation — {period}"), flush=True)

    return 0


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
