#!/usr/bin/env python3
"""Hold every CI job to the time budget in scripts/ci-job-budgets.tsv.

  ci-job-budgets.py check-workflow WORKFLOW BUDGETS
      Every job in WORKFLOW has exactly one budget, every budget names a job,
      a pull-request budget is at most the ceiling, and no budget is longer
      than the job's timeout-minutes.

  ci-job-budgets.py check-run BUDGETS JOBS_JSON [NOW]
      JOBS_JSON is GitHub's list of one run attempt's jobs
      (GET /repos/OWNER/REPO/actions/runs/ID/attempts/N/jobs). Prints every
      job's time and both of its limits. Warns when a job ran longer than its
      budget. Fails when a pull-request job ran longer than 20 minutes (the
      project's rule), when a nightly job ran longer than its budget, or when a
      job has no budget. A running job is judged by how long it has run so
      far, a cancelled one (a timeout ends that way) by how long it ran; a job
      that never started is printed as not judged. Each job's wait in the
      queue (started_at minus created_at) is printed but not judged. NOW
      (ISO 8601, for tests) replaces the current time.

Uses only the Python standard library, so it runs on any hosted runner.
"""

import datetime
import fnmatch
import json
import re
import sys

PR_CEILING_MINUTES = 17
RULE_MINUTES = 20


def load_budgets(path):
    budgets = []
    for number, line in enumerate(open(path, encoding="utf-8"), 1):
        line = line.rstrip("\n")
        if not line or line.startswith("#"):
            continue
        fields = line.split("\t")
        if len(fields) != 3 or not fields[0].isdigit() or fields[1] not in ("pr", "nightly"):
            sys.exit(f"{path}:{number}: want 'minutes<TAB>pr|nightly<TAB>job name', got {line!r}")
        budgets.append((int(fields[0]), fields[1], fields[2]))
    return budgets


def workflow_jobs(path):
    """Returns (pattern, timeout) for each job: the name with * for ${{ ... }}."""
    jobs, current, in_jobs = [], None, False
    for line in open(path, encoding="utf-8"):
        if line.rstrip() == "jobs:":
            in_jobs = True
            continue
        if not in_jobs:
            continue
        if re.match(r"^  [A-Za-z0-9_-]+:\s*$", line):
            current = {"id": line.strip()[:-1], "name": None, "timeout": None}
            jobs.append(current)
        elif current is not None:
            name = re.match(r"^    name:\s*(.+?)\s*$", line)
            timeout = re.match(r"^    timeout-minutes:\s*(\d+)\s*$", line)
            if name:
                current["name"] = re.sub(r"\$\{\{[^}]*\}\}", "*", name.group(1).strip("'\""))
            elif timeout:
                current["timeout"] = int(timeout.group(1))
    return [(job["name"] or job["id"], job["timeout"]) for job in jobs]


def check_workflow(workflow, budgets_path):
    budgets = load_budgets(budgets_path)
    by_name = {}
    problems = []
    for minutes, scope, name in budgets:
        if name in by_name:
            problems.append(f"{budgets_path} lists {name!r} twice")
        by_name[name] = (minutes, scope)
        if scope == "pr" and minutes > PR_CEILING_MINUTES:
            problems.append(f"{name!r} runs on pull requests, so its budget may be at most {PR_CEILING_MINUTES} minutes, not {minutes}")
    names = set()
    for name, timeout in workflow_jobs(workflow):
        names.add(name)
        if name not in by_name:
            problems.append(f"job {name!r} has no budget: add a line to {budgets_path}")
        elif timeout is not None and by_name[name][0] > timeout:
            problems.append(f"job {name!r} has a budget of {by_name[name][0]} minutes, longer than its timeout-minutes of {timeout}")
    for name in by_name:
        if name not in names:
            problems.append(f"{budgets_path} has a budget for {name!r}, but {workflow} has no such job: remove it")
    for problem in problems:
        print(problem, file=sys.stderr)
    if problems:
        return 1
    print(f"ci-job-budgets: {len(names)} jobs, each with one budget")
    return 0


def parse_time(value):
    return datetime.datetime.fromisoformat(value.replace("Z", "+00:00"))


def clock(seconds):
    seconds = int(seconds)
    return f"{seconds // 60}m{seconds % 60:02d}s"


def check_run(budgets_path, jobs_path, now=None):
    budgets = load_budgets(budgets_path)
    data = json.load(open(jobs_path, encoding="utf-8"))
    jobs = data["jobs"]
    if data.get("total_count", len(jobs)) > len(jobs):
        print(f"the run has {data['total_count']} jobs but the list holds {len(jobs)}: ask for more per page", file=sys.stderr)
        return 1
    now = parse_time(now) if now else datetime.datetime.now(datetime.timezone.utc)
    warnings, failures = [], []
    longest_queue = (0, "")
    for job in sorted(jobs, key=lambda j: j["name"]):
        name, conclusion = job["name"], job.get("conclusion")
        matches = [b for b in budgets if fnmatch.fnmatchcase(name, b[2])]
        if not matches:
            failures.append(f"job {name!r} has no budget in {budgets_path}")
            continue
        minutes, scope = matches[0][0], matches[0][1]
        # A pull-request job fails only above the project's rule; its budget,
        # which leaves room for a slow runner, warns. A nightly job's budget is
        # its limit.
        fail_minutes = RULE_MINUTES if scope == "pr" else minutes
        limits = f"warn above {minutes}m, fail above {fail_minutes}m"
        if conclusion == "skipped" or not job.get("started_at"):
            print(f"--   {'':>7s} not judged ({conclusion or job['status']}, no time)  {name}")
            continue
        running = job["status"] != "completed" or not job.get("completed_at")
        end = now if running else parse_time(job["completed_at"])
        seconds = (end - parse_time(job["started_at"])).total_seconds()
        # A job that hit its timeout-minutes ends as cancelled; it is judged by
        # how long it ran, like any other.
        state = "running" if running else (conclusion or "done")
        verb = "has run for" if running else "took"
        if seconds > fail_minutes * 60:
            verdict = "FAIL"
            failures.append(
                f"{name} {verb} {clock(seconds)}, over {fail_minutes} minutes"
                + (" (the project's 20-minute rule)" if scope == "pr" else f" (its budget in {budgets_path})")
                + ". Make it faster or move work to another job."
            )
        elif seconds > minutes * 60:
            verdict = "warn"
            warnings.append(
                f"{name} {verb} {clock(seconds)}, over its budget of {minutes} minutes in {budgets_path}"
                f" (it fails above {fail_minutes}). Make it faster, or raise the budget with a reason."
            )
        else:
            verdict = "ok"
        # Time in the queue is reported, not judged: it comes from the
        # account's limit on concurrent hosted jobs, not from the job.
        queued = ""
        if job.get("created_at"):
            wait = (parse_time(job["started_at"]) - parse_time(job["created_at"])).total_seconds()
            queued = f"; queued {clock(max(wait, 0))}"
            longest_queue = max(longest_queue, (wait, name))
        print(f"{verdict:4s} {clock(seconds):>7s} ({limits}{queued})  {name} ({state})")
    if longest_queue[1]:
        print(f"Longest wait in the queue: {clock(longest_queue[0])} ({longest_queue[1]}). Queue time is not judged.")
    for warning in warnings:
        print(f"::warning::{warning}")
    for failure in failures:
        print(f"::error::{failure}")
    return 1 if failures else 0


def main(argv):
    if len(argv) == 4 and argv[1] == "check-workflow":
        return check_workflow(argv[2], argv[3])
    if len(argv) in (4, 5) and argv[1] == "check-run":
        return check_run(argv[2], argv[3], argv[4] if len(argv) == 5 else None)
    print(__doc__, file=sys.stderr)
    return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
