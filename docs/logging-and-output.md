# Logging and output

Devsy exposes two separate streams of information:

- Status events describe the operation lifecycle (`started`, `succeeded`,
  `failed`, or `skipped`).
- Diagnostic logs describe technical details useful for troubleshooting.

## Human output

Human-readable status uses ASCII markers so it is safe in terminals, CI,
redirected output, and Windows shells:

```text
[RUN]  Build image
[OK]   Build image (8.2s)
[FAIL] Build image
```

Color is optional and never carries meaning. Status output does not use
spinners, Unicode glyphs, progress dots, or terminal animation.

## Structured output

Status events are newline-delimited JSON. The current schema is identified by
`schemaVersion: 1` and includes `state`, with optional operation IDs, duration,
and structured error information. This is the only supported status schema;
legacy lifecycle fields are not supported.

```json
{"kind":"status","schemaVersion":1,"phase":"building_image","state":"succeeded","durationMs":8214}
```

Status lines without `schemaVersion: 1` or without a recognized `state` are
ignored as diagnostic lines rather than renderer state.

Cancellation and timeout failures use the stable error codes `canceled` and
`deadline_exceeded`, respectively, with an actionable retry hint.

## Automation

Use structured output for scripts:

```bash
devsy workspace describe NAME --result-format json
devsy ... --log-output json
devsy ... --log-output logfmt
```

Do not parse ordinary human text. Command results remain on stdout; diagnostic
logs and human errors are written to stderr. Commands with an explicit NDJSON
event-stream contract may place status events on stdout as part of that
protocol. In that case, status lines may precede the final result line; a
consumer should read the stream line-by-line and select the `result` envelope
instead of parsing the complete stdout buffer as one JSON document.

`devsy ci` uses the same lifecycle events but renders them as deterministic
ASCII status lines on stderr, leaving the command running inside the
container free to use stdout for its own output.

## Diagnostics and secrets

Ordinary progress belongs to status events. Technical details belong at debug
or verbose level. Errors are returned to the command boundary and rendered
once to avoid duplicate messages.

Subprocess diagnostics are bounded to 200 lines or 1 MiB per stream. Secret
values from credential-bearing environment variables are redacted before
capture, rendering, persistence, or forwarding to Desktop.
