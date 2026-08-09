---
kind: task
id: agent-analytics
agent: agent-analytics
---

Run the `agent-analytics` agent daily to review the previous day's agent-fleet runs.

Gathers run telemetry from the OpenHands Cloud API (read-only, via `OPENHANDS_API_KEY`),
clusters failure modes with the Python data-science stack in `hack/analytics/analyze_runs.py`,
and ships one deterministic intervention (script, prompt, or small code fix) that lowers the
cognitive load agents spend recovering from errors — keeping reasoning budget on the primary
task. Persists findings to the running automation via the automation service.
