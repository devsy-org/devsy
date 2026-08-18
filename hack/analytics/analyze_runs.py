# /// script
# requires-python = ">=3.10"
# dependencies = [
#     "typer>=0.12",
#     "numpy>=1.26",
#     "scikit-learn>=1.4",
#     "pandas>=2.2",
#     "seaborn>=0.13",
#     "matplotlib>=3.8",
#     "prettytable>=3.10",
# ]
# ///
"""Deterministic agent-fleet run analyzer.

Gathers the previous day's agent runs from the OpenHands Cloud API (read-only),
normalizes them into tabular run records, clusters failure signatures with
scikit-learn, ranks recurring errors, renders a seaborn visualization, and emits
a human-readable daily report with concrete recommendations.

The goal is to reduce the cognitive load agents spend recovering from errors by
codifying the recurring failure modes the data surfaces, so an operator (or the
agent-analytics agent itself) can ship one deterministic intervention per day.

Run with uv (deps are declared inline above, so no venv or pip is needed):

    uv run hack/analytics/analyze_runs.py --sample --out-dir dist/analytics
    uv run hack/analytics/analyze_runs.py --since yesterday --until today

Auth uses OPENHANDS_API_KEY (Bearer). When the key is absent or no runs are
found in the window, pass --sample to exercise the full pipeline against bundled
synthetic data so the outputs are always producible offline.

Outputs (under --out-dir, default dist/analytics):
  runs.csv, failure_clusters.csv, top_errors.csv, daily_report.md,
  failure_heatmap.png
"""

from __future__ import annotations

import csv
import json
import os
import re
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid
import zipfile
from collections import Counter
from dataclasses import dataclass, field
from datetime import datetime, timedelta, timezone
from typing import Any

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import numpy as np
import pandas as pd
import seaborn as sns
import typer
from prettytable import PrettyTable
from sklearn.cluster import KMeans

app = typer.Typer(add_completion=False, help="Deterministic agent-fleet run analyzer.")

EVENTS_PAGE_SIZE: int = 100

FEATURE_BUCKETS: list[str] = [
    "missing-tool", "test-failure", "cmd-exit-nonzero", "permission-denied",
    "timeout", "git-baseline", "lint-issue", "commit-signing",
    "frontend-check", "go-panic", "other", "none",
]
WORD_TO_UNIT: dict[str, str] = {
    "day": "d", "hour": "h", "minute": "m", "second": "s",
    "week": "w", "month": "mo", "year": "y",
}
EPOCH_UNITS: set[str] = {"y", "mo", "w", "d", "h", "m", "s"}
ISO_FORMATS: tuple[str, ...] = (
    "%Y-%m-%d", "%Y-%m-%dT%H:%M:%S", "%Y-%m-%dT%H:%M:%SZ", "%Y-%m-%dT%H:%M:%S%z",
)
AGENT_BRANCH_RE = re.compile(r"([a-z][a-z0-9-]*?)/")
COMPACT_RE = re.compile(r"^(-?\d+)\s*([a-zA-Z]+)$")
WORD_RE = re.compile(r"^(\d+)\s+([a-zA-Z]+)(?:\s+ago)?$")
ERROR_RULES: list[tuple[tuple[str, ...], str]] = [
    (("command not found", "not installed", "no such file or directory"), "missing-tool"),
    (("exit code", "test"), "test-failure"),
    (("exit code",), "cmd-exit-nonzero"),
    (("permission denied", "403", "forbidden"), "permission-denied"),
    (("timeout", "timed out"), "timeout"),
    (("merge-base", "origin/main", "git fetch"), "git-baseline"),
    (("lint", "golangci", "biome"), "lint-issue"),
    (("sign_commit", "verified=false", "createcommitonbranch"), "commit-signing"),
    (("svelte-check", "vitest"), "frontend-check"),
    (("panic", "nil pointer"), "go-panic"),
]
SAMPLE_ROWS: tuple[tuple[Any, ...], ...] = (
    ("lint-fixer", "error", 1, 420.0, 38, 22, 3, "missing-tool", "golangci-lint: command not found"),
    ("lint-fixer", "ran", 0, 300.0, 30, 18, 0, "none", ""),
    ("ci-optimizer", "error", 1, 510.0, 44, 25, 2, "missing-tool", "act: command not found"),
    ("ci-optimizer", "error", 1, 490.0, 40, 21, 2, "git-baseline", "git merge-base origin/main: ambiguous"),
    ("pkg-core-agent", "error", 1, 600.0, 52, 30, 4, "test-failure", "TestRepoClone: exit code 1"),
    ("pkg-core-agent", "error", 1, 620.0, 55, 31, 4, "test-failure", "TestRepoClone: exit code 1"),
    ("pkg-core-agent", "ran", 0, 360.0, 28, 16, 0, "none", ""),
    ("pkg-ssh-git", "error", 1, 380.0, 33, 19, 1, "permission-denied", "permission denied (publickey)"),
    ("pkg-container", "error", 1, 700.0, 60, 35, 5, "cmd-exit-nonzero", "docker: Cannot connect to the Docker daemon"),
    ("pkg-container", "error", 1, 720.0, 62, 36, 5, "cmd-exit-nonzero", "docker: Cannot connect to the Docker daemon"),
    ("ui-polish", "error", 1, 290.0, 26, 14, 2, "frontend-check", "svelte-check: type error in App.svelte"),
    ("ui-polish", "ran", 0, 240.0, 22, 12, 0, "none", ""),
    ("docs-keeper", "ran", 0, 180.0, 16, 9, 0, "none", ""),
    ("codefactor", "error", 1, 410.0, 34, 20, 1, "lint-issue", "golangci-lint: SA9003: return value"),
    ("integration-test", "error", 1, 800.0, 70, 40, 3, "timeout", "context deadline exceeded"),
    ("devcontainer-spec", "error", 1, 330.0, 29, 16, 2, "commit-signing", "verified=false"),
)


@dataclass
class RunRecord:
    """One normalized row per agent run."""

    conversation_id: str
    agent: str
    status: str
    failed: int
    duration_s: float
    event_count: int
    tool_call_count: int
    error_count: int
    top_error_bucket: str
    error_buckets: str
    sample_error: str
    title: str


@dataclass
class ErrorStat:
    """Aggregated counts derived while scanning a run's events."""

    tool_counts: Counter[str] = field(default_factory=Counter)
    error_buckets: Counter[str] = field(default_factory=Counter)
    error_texts: list[str] = field(default_factory=list)
    error_events: int = 0


@dataclass
class AnalysisBundle:
    """Everything the output writers need, bundled to keep arg counts low."""

    runs: list[RunRecord]
    clusters: list[dict[str, Any]]
    tops: list[dict[str, Any]]
    window: tuple[str, str]
    source: str


@dataclass
class Options:
    """Resolved CLI options shared across helpers."""

    out_dir: str
    base_url: str
    token: str
    limit: int
    sample: bool
    verbose: bool


@dataclass
class OutputPaths:
    """Bundle of output file paths to keep writer arg counts low."""

    runs_csv: str
    clusters_csv: str
    top_csv: str
    report_md: str
    heatmap_png: str


@dataclass
class UploadResult:
    """Outcome of a temp.sh upload (None link when upload is skipped/failed)."""

    link: str | None
    reason: str


def parse_when(value: str, now: datetime) -> datetime:
    """Parse a date/datetime or relative phrase into a timezone-aware datetime."""
    low = value.strip().lower()
    named = named_when(low, now)
    if named is not None:
        return named
    compact = match_compact(low, now)
    if compact is not None:
        return compact
    worded = match_words(low, now)
    if worded is not None:
        return worded
    iso_dt = parse_iso(value)
    if iso_dt is not None:
        return iso_dt
    raise typer.BadParameter(f"unparseable date/time: {value!r}")


def named_when(low: str, now: datetime) -> datetime | None:
    """Resolve the named aliases: now/today/yesterday/tomorrow."""
    table: dict[str, datetime] = {
        "now": now,
        "today": now.replace(hour=0, minute=0, second=0, microsecond=0),
        "yesterday": (now - timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0),
        "tomorrow": (now + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0),
        "eod": (now + timedelta(days=1)).replace(hour=0, minute=0, second=0, microsecond=0),
    }
    return table.get(low)


def match_compact(low: str, now: datetime) -> datetime | None:
    """Resolve compact signed relatives like -2d, 3h, -1w."""
    m = COMPACT_RE.match(low)
    if not m:
        return None
    unit = WORD_TO_UNIT.get(m.group(2).rstrip("s"), m.group(2).rstrip("s"))
    if unit not in EPOCH_UNITS:
        return None
    return add_relative(now, int(m.group(1)), unit)


def match_words(low: str, now: datetime) -> datetime | None:
    """Resolve word relatives like '2 days ago', '3 hours', '1 week ago'."""
    m = WORD_RE.match(low)
    if not m:
        return None
    unit = WORD_TO_UNIT.get(m.group(2).rstrip("s"), m.group(2).rstrip("s"))
    if unit not in EPOCH_UNITS:
        return None
    return add_relative(now, -int(m.group(1)), unit)


def parse_iso(value: str) -> datetime | None:
    """Parse an ISO 8601 date/datetime string, defaulting to UTC when naive."""
    for fmt in ISO_FORMATS:
        try:
            dt = datetime.strptime(value, fmt)
        except ValueError:
            continue
        return dt if dt.tzinfo else dt.replace(tzinfo=timezone.utc)
    return None


def add_relative(base: datetime, n: int, unit: str) -> datetime:
    """Add a relative offset to a base datetime, keyed by epoch unit."""
    deltas: dict[str, timedelta] = {
        "d": timedelta(days=n), "h": timedelta(hours=n), "m": timedelta(minutes=n),
        "s": timedelta(seconds=n), "w": timedelta(weeks=n),
        "mo": timedelta(days=30 * n), "y": timedelta(days=365 * n),
    }
    delta = deltas.get(unit)
    if delta is None:
        raise typer.BadParameter(f"unknown relative unit: {unit!r}")
    return base + delta


def iso(dt: datetime) -> str:
    """Render a datetime as a UTC ISO 8601 string."""
    return dt.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class CloudClient:
    """Minimal read-only OpenHands Cloud API client."""

    def __init__(self, base_url: str, token: str, timeout: int = 30, verbose: bool = False) -> None:
        self.base = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout
        self.verbose = verbose

    def search_conversations(self, limit: int = 100) -> list[dict[str, Any]]:
        """List recent app conversations."""
        data = self.get("/api/v1/app-conversations/search", {"limit": limit})
        return normalize_items(data)

    def conversation_events(self, conv_id: str, limit: int = 500) -> list[dict[str, Any]]:
        """Fetch events for one conversation, paging through the API.

        The events endpoint caps ``limit`` at 100 per request, so a single
        call with a larger limit is rejected (HTTP 422). Page in batches of
        ``EVENTS_PAGE_SIZE`` using the ``page_id`` cursor until ``limit`` is
        reached or the server signals the end (``next_page_id`` is empty).
        """
        path = f"/api/v1/conversation/{conv_id}/events/search"
        collected: list[dict[str, Any]] = []
        cursor: str | None = None
        while len(collected) < limit:
            page_size = min(EVENTS_PAGE_SIZE, limit - len(collected))
            params: dict[str, Any] = {"limit": page_size}
            if cursor is not None:
                params["page_id"] = cursor
            data = self.get(path, params)
            if not isinstance(data, dict):
                collected.extend(normalize_items(data))
                break
            collected.extend(normalize_items(data.get("items")))
            cursor = data.get("next_page_id")
            if not cursor or len(normalize_items(data.get("items"))) < page_size:
                break
        return collected

    def get(self, path: str, params: dict[str, Any]) -> Any:
        """Issue a GET request and decode JSON, raising on HTTP/parse errors."""
        url = self.build_url(path, params)
        if self.verbose:
            typer.echo(f"GET {url}", err=True)
        req = urllib.request.Request(url, headers=self.headers())
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                body = resp.read()
        except urllib.error.HTTPError as exc:
            raise typer.Exit(1) from SystemExit(f"HTTP {exc.code} for {url}")
        return decode_json(url, body)

    def build_url(self, path: str, params: dict[str, Any]) -> str:
        """Build a query-stringed URL for a path."""
        qs = urllib.parse.urlencode(params)
        return f"{self.base}{path}?{qs}"

    def headers(self) -> dict[str, str]:
        """Build auth + accept headers."""
        out: dict[str, str] = {"Accept": "application/json"}
        if self.token:
            out["Authorization"] = f"Bearer {self.token}"
        return out


def normalize_items(data: Any) -> list[dict[str, Any]]:
    """Normalize an API response into a list of item dicts."""
    if isinstance(data, dict):
        items = data.get("items", [])
    else:
        items = data or []
    return [i for i in items if isinstance(i, dict)]


def decode_json(url: str, body: bytes) -> Any:
    """Decode JSON bytes, raising on empty or malformed payloads."""
    if not body:
        return None
    try:
        return json.loads(body)
    except json.JSONDecodeError as exc:
        raise typer.Exit(1) from SystemExit(f"non-JSON response from {url}: {exc}")


def infer_agent(conversation: dict[str, Any]) -> str:
    """Derive the owning agent from branch/title/git context."""
    for key in ("git_branch_name", "gitBranchName", "branch", "head_branch"):
        m = AGENT_BRANCH_RE.match(str(conversation.get(key) or ""))
        if m:
            return m.group(1)
    title = conversation.get("title") or conversation.get("name") or ""
    m = AGENT_BRANCH_RE.search(str(title))
    if m:
        return m.group(1)
    return fallback_agent(conversation)


def fallback_agent(conversation: dict[str, Any]) -> str:
    """Return a last-resort agent identifier from agent_id/repo."""
    return str(conversation.get("agent_id") or conversation.get("repo") or "unknown")


def parse_ts(value: str | None) -> datetime | None:
    """Parse an ISO timestamp string into a timezone-aware datetime."""
    if not value:
        return None
    try:
        dt = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError:
        return None
    return dt if dt.tzinfo else dt.replace(tzinfo=timezone.utc)


def classify_error(text: str) -> str:
    """Bucket a raw error string into a canonical failure signature."""
    low = (text or "").lower()
    for needles, label in ERROR_RULES:
        if all(n in low for n in needles):
            return label
    if text:
        return "other"
    return "none"


def extract_error_text(event: dict[str, Any]) -> str:
    """Pull the most useful error text from an event's common shapes."""
    for key in ("error", "detail", "message", "stderr"):
        v = event.get(key)
        if isinstance(v, str) and v:
            return v
    nested = nested_dict(event, "observation")
    for key in ("stderr", "stdout", "content", "message"):
        v = nested.get(key)
        if isinstance(v, str) and v:
            return v
    action = nested_dict(event, "action")
    cmd = action.get("command")
    return cmd if isinstance(cmd, str) else ""


def nested_dict(event: dict[str, Any], key: str) -> dict[str, Any]:
    """Return event[key] as a dict, or an empty dict when absent/not a dict."""
    v = event.get(key)
    return v if isinstance(v, dict) else {}


def build_run_record(conversation: dict[str, Any], events: list[dict[str, Any]]) -> RunRecord:
    """Normalize a conversation + its events into a RunRecord."""
    stat = scan_events(events)
    started = parse_ts(conversation.get("created_at") or conversation.get("createdAt"))
    ended = parse_ts(conversation.get("updated_at") or conversation.get("updatedAt"))
    duration = duration_s(started, ended)
    status = resolve_status(conversation, stat.error_events)
    return RunRecord(
        conversation_id=str(conversation.get("id") or conversation.get("app_conversation_id") or ""),
        agent=infer_agent(conversation),
        status=status,
        failed=int(is_failed(status, stat.error_events)),
        duration_s=round(duration, 1),
        event_count=len(events),
        tool_call_count=sum(stat.tool_counts.values()),
        error_count=stat.error_events,
        top_error_bucket=stat.error_buckets.most_common(1)[0][0] if stat.error_buckets else "none",
        error_buckets=json.dumps(stat.error_buckets, separators=(",", ":")),
        sample_error=stat.error_texts[0] if stat.error_texts else "",
        title=str(conversation.get("title") or ""),
    )


def scan_events(events: list[dict[str, Any]]) -> ErrorStat:
    """Tally tool calls and error signals across a run's events."""
    stat = ErrorStat()
    for event in events:
        kind = event.get("kind") or ""
        tool = event.get("tool_name") or ""
        if tool:
            stat.tool_counts[str(tool)] += 1
        if kind in {"ErrorEvent", "ObservationEvent"}:
            maybe_count_error(event, stat)
    return stat


def maybe_count_error(event: dict[str, Any], stat: ErrorStat) -> None:
    """Record an error bucket/text when an event looks like a failure."""
    text = extract_error_text(event)
    exit_code = event_exit_code(event)
    if looks_like_error(event.get("kind"), text, exit_code):
        stat.error_events += 1
        stat.error_buckets[classify_error(text)] += 1
        if text:
            stat.error_texts.append(text[:300])


def event_exit_code(event: dict[str, Any]) -> int | None:
    """Read the exit code from either the observation or the event top level."""
    obs = nested_dict(event, "observation")
    code = obs.get("exit_code", event.get("exit_code"))
    return to_int(code)


def to_int(value: Any) -> int | None:
    """Coerce a value to int, returning None when not convertible."""
    if isinstance(value, bool) or value is None:
        return None
    try:
        return int(value)
    except (TypeError, ValueError):
        return None


def looks_like_error(kind: Any, text: str, exit_code: int | None) -> bool:
    """Decide whether an event represents a failure."""
    if kind == "ErrorEvent":
        return True
    if text and exit_code not in (None, 0):
        return True
    return "error" in (text or "").lower()


def duration_s(started: datetime | None, ended: datetime | None) -> float:
    """Compute a run duration in seconds from its bounds."""
    if not started or not ended:
        return 0.0
    return (ended - started).total_seconds()


def resolve_status(conversation: dict[str, Any], error_events: int) -> str:
    """Resolve a human-readable run status."""
    status = conversation.get("execution_status") or conversation.get("status") or "unknown"
    if status == "unknown" and error_events == 0:
        return "stopped" if conversation.get("stopped") else "ran"
    return str(status)


def is_failed(status: str, error_events: int) -> bool:
    """Decide whether a run counts as failed."""
    return status in {"error", "stopped", "aborted", "failed"} or error_events > 0


def fetch_runs(client: CloudClient, window: tuple[datetime, datetime], limit: int) -> list[RunRecord]:
    """Fetch and normalize all runs in a window from the Cloud API."""
    since, until = window
    runs: list[RunRecord] = []
    for conv in client.search_conversations(limit=limit):
        if in_window(conv, since, until):
            runs.append(run_from_conv(client, conv))
        time.sleep(0.05)
    return runs


def in_window(conv: dict[str, Any], since: datetime, until: datetime) -> bool:
    """Check whether a conversation was created inside the window."""
    created = parse_ts(conv.get("created_at") or conv.get("createdAt"))
    return bool(created and since <= created < until)


def run_from_conv(client: CloudClient, conv: dict[str, Any]) -> RunRecord:
    """Build a RunRecord for a conversation, fetching its events."""
    cid = conv.get("id") or conv.get("app_conversation_id")
    if not cid:
        return build_run_record(conv, [])
    events = client.conversation_events(str(cid), limit=500)
    return build_run_record(conv, events)


def synthetic_runs() -> list[RunRecord]:
    """Bundled synthetic sample so the pipeline produces outputs offline."""
    out: list[RunRecord] = []
    for i, row in enumerate(SAMPLE_ROWS):
        agent, status, failed, dur, ev, tools, errs, bucket, sample = row
        out.append(RunRecord(
            conversation_id=f"sample-{i:03d}",
            agent=agent,
            status=status,
            failed=failed,
            duration_s=dur,
            event_count=ev,
            tool_call_count=tools,
            error_count=errs,
            top_error_bucket=bucket,
            error_buckets=json.dumps({bucket: errs} if errs else {}, separators=(",", ":")),
            sample_error=sample,
            title=f"{agent} run {i}",
        ))
    return out


def to_row(r: RunRecord) -> dict[str, Any]:
    """Convert a RunRecord into a plain dict for CSV writing."""
    return {
        "conversation_id": r.conversation_id, "agent": r.agent, "status": r.status,
        "failed": r.failed, "duration_s": r.duration_s, "event_count": r.event_count,
        "tool_call_count": r.tool_call_count, "error_count": r.error_count,
        "top_error_bucket": r.top_error_bucket, "error_buckets": r.error_buckets,
        "sample_error": r.sample_error, "title": r.title,
    }


def bucket_map() -> tuple[list[str], dict[str, int]]:
    """Return the bucket list and an index map for the feature vector."""
    return FEATURE_BUCKETS, {b: i for i, b in enumerate(FEATURE_BUCKETS)}


def feature_vector(r: RunRecord, b_idx: dict[str, int]) -> np.ndarray:
    """Build the failure-bucket feature vector for one run."""
    vec = np.zeros(len(b_idx), dtype=float)
    try:
        eb = json.loads(r.error_buckets) if r.error_buckets else {}
    except json.JSONDecodeError:
        eb = {}
    for bucket, count in eb.items():
        if bucket in b_idx:
            vec[b_idx[bucket]] += float(count)
    if not r.error_count and "none" in b_idx:
        vec[b_idx["none"]] = 1.0
    return vec


def cluster_failures(runs: list[RunRecord]) -> list[dict[str, Any]]:
    """KMeans-cluster runs by their failure-bucket feature vector."""
    if not runs:
        return []
    _, b_idx = bucket_map()
    matrix = np.vstack([feature_vector(r, b_idx) for r in runs])
    labels = fit_labels(matrix)
    return [cluster_row(runs[i], int(labels[i])) for i in range(len(runs))]


def fit_labels(matrix: np.ndarray) -> np.ndarray:
    """Fit KMeans (or fall back to a single cluster) and return labels."""
    if matrix.shape[0] <= 1 or matrix.sum() == 0:
        return np.zeros(matrix.shape[0], dtype=int)
    k = max(2, min(5, matrix.shape[0] - 1))
    km = KMeans(n_clusters=k, n_init=10, random_state=7)
    return km.fit_predict(matrix)


def cluster_row(r: RunRecord, cluster: int) -> dict[str, Any]:
    """Build one failure_clusters.csv row."""
    return {
        "conversation_id": r.conversation_id, "agent": r.agent, "cluster": cluster,
        "error_count": r.error_count, "top_error_bucket": r.top_error_bucket,
        "sample_error": r.sample_error[:200],
    }


def top_errors(runs: list[RunRecord]) -> list[dict[str, Any]]:
    """Rank recurring failure signatures by run count."""
    sigs: Counter[str] = Counter()
    agents: dict[str, set[str]] = {}
    for r in runs:
        if not r.error_count:
            continue
        sigs[r.top_error_bucket] += 1
        agents.setdefault(r.top_error_bucket, set()).add(r.agent)
    failed_total = sum(1 for r in runs if r.error_count)
    return [top_row(sig, count, agents, failed_total) for sig, count in sigs.most_common()]


def top_row(sig: str, count: int, agents: dict[str, set[str]], failed_total: int) -> dict[str, Any]:
    """Build one top_errors.csv row."""
    return {
        "signature": sig, "run_count": count,
        "affected_agents": ",".join(sorted(agents.get(sig, set()))),
        "share_of_failed": round(count / max(1, failed_total), 3),
    }


def write_csv(path: str, rows: list[dict[str, Any]]) -> None:
    """Write a list of dicts to a CSV file."""
    fields = list(rows[0].keys()) if rows else []
    with open(path, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fields)
        writer.writeheader()
        for row in rows:
            writer.writerow(row)


def agent_index(runs: list[RunRecord]) -> tuple[list[str], dict[str, int]]:
    """Return sorted agent names and their row index."""
    agents = sorted({r.agent for r in runs})
    return agents, {a: i for i, a in enumerate(agents)}


def bucket_matrix(runs: list[RunRecord]) -> tuple[list[str], list[str], np.ndarray]:
    """Build the (agents, buckets, matrix) counts of failures by agent x bucket."""
    agents, a_idx = agent_index(runs)
    _, b_idx = bucket_map()
    matrix = np.zeros((len(agents), len(b_idx)), dtype=int)
    for r in runs:
        accumulate(r, a_idx, b_idx, matrix)
    return agents, FEATURE_BUCKETS, matrix


def accumulate(r: RunRecord, a_idx: dict[str, int], b_idx: dict[str, int], matrix: np.ndarray) -> None:
    """Add one run's bucket counts into the agent x bucket matrix."""
    if r.agent not in a_idx:
        return
    try:
        eb = json.loads(r.error_buckets) if r.error_buckets else {}
    except json.JSONDecodeError:
        eb = {}
    for bucket, count in eb.items():
        if bucket in b_idx:
            matrix[a_idx[r.agent], b_idx[bucket]] += int(count)


def render_heatmap(path: str, runs: list[RunRecord]) -> None:
    """Render the failure-mode x agent seaborn heatmap to a PNG."""
    if not runs:
        return
    agents, buckets, matrix = bucket_matrix(runs)
    if not agents:
        return
    df = pd.DataFrame(matrix, index=agents, columns=buckets)
    fig, ax = plt.subplots(figsize=fig_size(buckets, agents))
    sns.heatmap(df, annot=True, fmt="d", cmap="rocket_r", ax=ax, cbar=True)
    ax.set_title("agent-fleet failure modes (yesterday)")
    ax.set_xlabel("failure signature")
    ax.set_ylabel("agent")
    fig.tight_layout()
    fig.savefig(path, dpi=140)
    plt.close(fig)


def fig_size(buckets: list[str], agents: list[str]) -> tuple[float, float]:
    """Pick a readable figure size from the matrix dimensions."""
    return (max(8.0, len(buckets) * 0.7), max(4.0, len(agents) * 0.5))


def make_table(columns: list[str], rows: list[list[Any]]) -> PrettyTable:
    """Build a PrettyTable with a left-aligned first column."""
    table = PrettyTable()
    table.field_names = columns
    table.align = "l"
    for row in rows:
        table.add_row(row)
    return table


def write_report(path: str, bundle: AnalysisBundle) -> None:
    """Write the human-readable daily report markdown (footer appended later)."""
    lines = report_header(bundle)
    lines.append(report_agent_table(bundle.runs))
    lines.append(report_top_table(bundle.tops))
    lines.append(report_cluster_table(bundle.clusters))
    lines.append(report_recommendations(bundle.tops))
    with open(path, "w") as f:
        f.write("\n\n".join(lines))


def report_header(bundle: AnalysisBundle) -> list[str]:
    """Build the report title + headline metrics."""
    total = len(bundle.runs)
    failed = sum(1 for r in bundle.runs if r.error_count)
    rate = round(failed / total, 3) if total else 0.0
    return [
        "# agent-fleet daily report",
        "",
        f"- window: `{bundle.window[0]}` -> `{bundle.window[1]}`",
        f"- source: {bundle.source}",
        f"- runs: {total}",
        f"- failed runs: {failed}",
        f"- failure rate: {rate}",
    ]


def report_agent_table(runs: list[RunRecord]) -> str:
    """Build the per-agent breakdown as a PrettyTable block."""
    by = per_agent(runs)
    rows = [[agent, d["runs"], d["failed"], d["errors"]] for agent, d in sorted(by.items())]
    table = make_table(["agent", "runs", "failed", "errors"], rows)
    return "## per-agent breakdown\n\n```\n" + str(table.get_string()) + "\n```"


def per_agent(runs: list[RunRecord]) -> dict[str, dict[str, int]]:
    """Aggregate per-agent run/failed/error counts."""
    out: dict[str, dict[str, int]] = {}
    for r in runs:
        d = out.setdefault(r.agent, {"runs": 0, "failed": 0, "errors": 0})
        d["runs"] += 1
        d["failed"] += 1 if r.error_count else 0
        d["errors"] += int(r.error_count)
    return out


def report_top_table(tops: list[dict[str, Any]]) -> str:
    """Build the top recurring failure signatures as a PrettyTable block."""
    rows = [[t["signature"], t["run_count"], t["affected_agents"], t["share_of_failed"]] for t in tops]
    table = make_table(["signature", "run_count", "affected_agents", "share_of_failed"], rows)
    return "## top recurring failure signatures\n\n```\n" + str(table.get_string()) + "\n```"


def report_cluster_table(clusters: list[dict[str, Any]]) -> str:
    """Build the failure clusters as a PrettyTable block."""
    rows = [[c["conversation_id"], c["agent"], c["cluster"], c["error_count"], c["top_error_bucket"]] for c in clusters]
    table = make_table(["conversation_id", "agent", "cluster", "error_count", "top_error_bucket"], rows)
    return "## failure clusters\n\n```\n" + str(table.get_string()) + "\n```"


def report_recommendations(tops: list[dict[str, Any]]) -> str:
    """Build the recommendations section from the ranked signatures."""
    if not tops:
        return "## recommendations\n\n- no actionable failure mode found today."
    top = tops[0]
    lines = [
        "## recommendations",
        "",
        f"- highest-impact: **{top['signature']}** affects {top['run_count']} runs across "
        f"{top['affected_agents']} (share_of_failed={top['share_of_failed']}). "
        "Pick ONE deterministic intervention that codifies recovery for this signature "
        "(missing-tool pre-flight check, prompt clarification, or small code fix) so agent "
        "reasoning budget stays on the primary task.",
    ]
    for t in tops[1:3]:
        lines.append(f"- secondary: **{t['signature']}** ({t['run_count']} runs, agents: {t['affected_agents']}).")
    return "\n".join(lines)


def gather_runs(opts: Options, window: tuple[datetime, datetime]) -> tuple[list[RunRecord], str]:
    """Return runs + their source label, honoring --sample or the live API."""
    if opts.sample:
        return synthetic_runs(), "synthetic-sample (--sample)"
    if not opts.token:
        typer.echo("OPENHANDS_API_KEY not set; pass --sample for offline mode.", err=True)
        raise typer.Exit(2)
    client = CloudClient(opts.base_url, opts.token, verbose=opts.verbose)
    runs = fetch_runs(client, window, opts.limit)
    if not runs:
        typer.echo("no runs found in window; retry with --sample for offline mode.", err=True)
        raise typer.Exit(3)
    return runs, "openhands-cloud-api"


def is_actionable(tops: list[dict[str, Any]], min_runs: int = 2, min_share: float = 0.1) -> bool:
    """Decide whether the data justifies a PR (a recurring, non-trivial signature)."""
    if not tops:
        return False
    top = tops[0]
    enough_runs = top.get("run_count", 0) >= min_runs
    enough_share = top.get("share_of_failed", 0.0) >= min_share
    return bool(enough_runs and enough_share)


def zip_outputs(paths: OutputPaths) -> str:
    """Zip the five output files into a temp archive and return its path."""
    handle, tmp = tempfile.mkstemp(suffix=".zip")
    os.close(handle)
    with zipfile.ZipFile(tmp, "w", zipfile.ZIP_DEFLATED) as zf:
        for p in (paths.runs_csv, paths.clusters_csv, paths.top_csv, paths.report_md, paths.heatmap_png):
            if os.path.exists(p):
                zf.write(p, arcname=os.path.basename(p))
    return tmp


def upload_to_tempsh(zip_path: str) -> UploadResult:
    """Upload the zipped outputs to temp.sh and return the share link."""
    try:
        with open(zip_path, "rb") as f:
            body = f.read()
    except OSError as exc:
        return UploadResult(link=None, reason=f"read failed: {exc}")
    boundary = uuid.uuid4().hex
    parts = [
        f"--{boundary}".encode(),
        f'Content-Disposition: form-data; name="file"; filename="{os.path.basename(zip_path)}"'.encode(),
        b"Content-Type: application/zip",
        b"",
        body,
        f"--{boundary}--".encode(),
        b"",
    ]
    payload = b"\r\n".join(parts)
    headers = {
        "Content-Type": f"multipart/form-data; boundary={boundary}",
        "User-Agent": "devsy-analytics/1.0",
        "Accept": "text/plain, */*",
    }
    req = urllib.request.Request("https://temp.sh/upload", data=payload, headers=headers, method="POST")
    try:
        with no_redirect_opener().open(req, timeout=60) as resp:
            return upload_response(resp)
    except urllib.error.HTTPError as exc:
        return UploadResult(link=None, reason=upload_http_error(exc))
    except (urllib.error.URLError, OSError) as exc:
        return UploadResult(link=None, reason=f"upload failed: {exc}")


def upload_response(resp: Any) -> UploadResult:
    """Inspect the temp.sh response (plain-text URL on success, short message on rejection)."""
    code = resp.getcode()
    raw = resp.read().decode("utf-8", errors="replace").strip()
    if code != 200:
        return UploadResult(link=None, reason=f"temp.sh returned HTTP {code}: {raw[:200]}")
    if not raw:
        return UploadResult(link=None, reason="temp.sh returned an empty body")
    if raw.startswith("http://") or raw.startswith("https://"):
        return UploadResult(link=raw, reason="uploaded to temp.sh")
    return UploadResult(link=None, reason=f"temp.sh rejected: {raw[:200]}")


def no_redirect_opener() -> urllib.request.OpenerDirector:
    """Build a minimal urllib opener (redirects are rejected by upload_response via code checks)."""
    opener = urllib.request.OpenerDirector()
    for handler in (urllib.request.HTTPSHandler(), urllib.request.HTTPHandler()):
        opener.add_handler(handler)
    return opener


def upload_http_error(exc: urllib.error.HTTPError) -> str:
    """Render a clear skip reason for a temp.sh HTTP error status."""
    return f"temp.sh returned HTTP {exc.code}"


def annotate_report_actionability(path: str, actionable: bool, upload: UploadResult) -> None:
    """Append the actionability + upload verdict and footer to the daily report."""
    verdict = "ACTIONABLE" if actionable else "NOT-ACTIONABLE"
    block = ["", "## pr-gate", "", f"- verdict: **{verdict}**"]
    if actionable:
        block.append("- a PR is warranted: ship the recommended intervention and reference the metric above.")
    else:
        block.append("- no PR: the data does not surface a recurring, high-share failure mode today.")
    if upload.link:
        block.append(f"- uploaded outputs: {upload.link}")
    else:
        block.append(f"- upload: skipped ({upload.reason})")
    block.append("")
    block.append("_generated by hack/analytics/analyze_runs.py_")
    block.append("")
    with open(path, "a") as f:
        f.write("\n".join(block))


def write_outputs(out_dir: str, bundle: AnalysisBundle) -> OutputPaths:
    """Write all five deliverables to disk and report what was written."""
    paths = output_paths(out_dir)
    write_csv(paths.runs_csv, [to_row(r) for r in bundle.runs])
    write_csv(paths.clusters_csv, bundle.clusters)
    write_csv(paths.top_csv, bundle.tops)
    render_heatmap(paths.heatmap_png, bundle.runs)
    write_report(paths.report_md, bundle)
    echo_writes(paths, bundle)
    return paths


def finalize_outputs(paths: OutputPaths, tops: list[dict[str, Any]]) -> None:
    """Upload the zipped outputs and append the actionability verdict to the report."""
    actionable = is_actionable(tops)
    upload = skip_upload() if no_upload_env() else upload_bundle(paths)
    annotate_report_actionability(paths.report_md, actionable, upload)
    echo_verdict(actionable, upload)


def no_upload_env() -> bool:
    """Read the ANALYTICS_NO_UPLOAD env flag."""
    return os.environ.get("ANALYTICS_NO_UPLOAD", "").lower() in {"1", "true", "yes"}


def skip_upload() -> UploadResult:
    """Return an UploadResult indicating the upload was intentionally skipped."""
    return UploadResult(link=None, reason="ANALYTICS_NO_UPLOAD=1")


def upload_bundle(paths: OutputPaths) -> UploadResult:
    """Zip the outputs, upload to temp.sh, and clean up the temp archive."""
    zip_path = zip_outputs(paths)
    try:
        return upload_to_tempsh(zip_path)
    finally:
        try:
            os.remove(zip_path)
        except OSError:
            pass


def echo_verdict(actionable: bool, upload: UploadResult) -> None:
    """Print the PR-gate verdict and upload outcome."""
    verdict = "ACTIONABLE" if actionable else "NOT-ACTIONABLE"
    typer.echo(f"pr-gate: {verdict}")
    if upload.link:
        typer.echo(f"uploaded outputs: {upload.link}")
    else:
        typer.echo(f"upload: skipped ({upload.reason})", err=True)


def output_paths(out_dir: str) -> OutputPaths:
    """Build the OutputPaths bundle for one output directory."""
    return OutputPaths(
        runs_csv=os.path.join(out_dir, "runs.csv"),
        clusters_csv=os.path.join(out_dir, "failure_clusters.csv"),
        top_csv=os.path.join(out_dir, "top_errors.csv"),
        report_md=os.path.join(out_dir, "daily_report.md"),
        heatmap_png=os.path.join(out_dir, "failure_heatmap.png"),
    )


def echo_writes(paths: OutputPaths, bundle: AnalysisBundle) -> None:
    """Print a summary of the written outputs."""
    typer.echo(f"wrote {paths.runs_csv} ({len(bundle.runs)} runs)")
    typer.echo(f"wrote {paths.clusters_csv} ({len(bundle.clusters)} clusters)")
    typer.echo(f"wrote {paths.top_csv} ({len(bundle.tops)} signatures)")
    typer.echo(f"wrote {paths.report_md}")
    typer.echo(f"wrote {paths.heatmap_png}")


def default_options(out_dir: str, sample: bool) -> Options:
    """Build Options from the CLI flags plus environment-derived values."""
    base = os.environ.get("OPENHANDS_HOST", "https://app.all-hands.dev")
    token = os.environ.get("OPENHANDS_API_KEY") or os.environ.get("OPENHANDS_CLOUD_API_KEY", "")
    verbose = os.environ.get("ANALYTICS_VERBOSE", "").lower() in {"1", "true", "yes"}
    return Options(out_dir=out_dir, base_url=base, token=token, limit=100, sample=sample, verbose=verbose)


@app.command()
def analyze(
    since: str = typer.Option("yesterday", help="window start: yesterday|today|ISO|'2 days ago'|'-2d' (use = for signed)."),
    until: str = typer.Option("today", help="window end: today|now|ISO|'1 day ago'|'-1d' (use = for signed)."),
    out_dir: str = typer.Option("dist/analytics", help="output directory."),
    sample: bool = typer.Option(False, "--sample", help="use bundled synthetic runs (offline)."),
) -> None:
    """Gather, cluster, rank, visualize, upload, and report on agent-fleet runs."""
    opts = default_options(out_dir, sample)
    os.makedirs(opts.out_dir, exist_ok=True)
    now = datetime.now(timezone.utc)
    window_dt = (parse_when(since, now), parse_when(until, now))
    window = (iso(window_dt[0]), iso(window_dt[1]))
    runs, source = gather_runs(opts, window_dt)
    clusters = cluster_failures(runs)
    tops = top_errors(runs)
    bundle = AnalysisBundle(runs=runs, clusters=clusters, tops=tops, window=window, source=source)
    paths = write_outputs(opts.out_dir, bundle)
    finalize_outputs(paths, tops)


if __name__ == "__main__":
    app()
