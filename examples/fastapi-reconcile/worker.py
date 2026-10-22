"""The other entrypoint.

`toft.toml` declares this as the `close` job, and the platform runs it as
an isolated workload: its own container, its own memory limit, its own deadline,
and a durable record whether it succeeds or not. Same repository, same release,
same storage — a different command.

Run it by hand too. `python worker.py 2026-06` works from a checkout, which is
the property that makes a job testable at all.
"""

from __future__ import annotations

import sys

import engine
import ledger
import platform_seam as seam

try:
    import toft
except Exception:                                  # pragma: no cover
    toft = None                                      # type: ignore[assignment]


def main(argv: list[str]) -> int:
    # `toft.run` is the job's own view of itself, and it is None everywhere else.
    # That is how one module shared between two entrypoints tells which one it
    # is in, without a flag anybody has to remember to pass.
    run = getattr(toft, "run", None) if toft else None
    params = run.params if run else {}

    period = (params.get("period")
              or (argv[1] if len(argv) > 1 else "")
              or seam.settings()[0]["period"])

    cfg, _version, _note = seam.settings()
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

    print(f"Closing {period}", flush=True)
    print(f"  tolerance   {cfg['tolerance_pence']}p", flush=True)
    print(f"  date window {cfg['date_window_days']} days", flush=True)
    print(f"  register    {ledger.where().path}  ({ledger.where().source})", flush=True)

    result = engine.run_close(
        period,
        tolerance_pence=int(cfg["tolerance_pence"]),
        date_window_days=int(cfg["date_window_days"]),
        trigger="job",
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
