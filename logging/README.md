# Shared logging

This module provides structured logging and credential masking for Go producers. It uses only the standard library and supports Go 1.23. It can be used independently of the Core and Runtime SDK modules. Use an approved merged commit or published submodule version when adding a dependency. Do not depend on an unpublished local checkout.

## Configure a producer

Inject the diagnostic writer, environment, and known secret values. The caller owns configuration errors, warnings, output streams, and logger lifetime.

```go
import (
    "errors"
    "io"
    "log/slog"

    "github.com/devsy-org/devsy/logging"
    "github.com/devsy-org/devsy/logging/redact"
)

func newLogger(out io.Writer, level, format string, environment, secrets []string) (*slog.Logger, error) {
    config, invalid := logging.ResolveProvider(level, format)
    if invalid.Level || invalid.Format {
        return nil, errors.New("invalid logging configuration")
    }
    redactor := redact.New(redact.Options{Environment: environment, Values: secrets})
    handler := logging.NewHandler(out, logging.HandlerOptions{
        Level: config.Level,
        Format: config.Format,
        Redactor: redactor,
    })
    return slog.New(handler), nil
}
```

`ResolveProvider` returns info and JSON for empty or invalid settings and reports invalid fields separately. The caller decides whether to reject invalid settings or warn and use the fallback. Values are case insensitive and are not trimmed. Accepted formats are `auto`, `json`, `text`, `plain`, and `logfmt`. `auto` resolves to JSON and `plain` selects text. The caller chooses any terminal-specific format or presentation before constructing the handler.

JSON records use `ts`, `level`, and `msg` with structured attributes. The handler writes only to the supplied writer. Send diagnostics to stderr or another dedicated sink. Keep stdout and transported command bytes reserved for their protocols. The handler does not configure process globals or terminate the process for fatal severity.

## Redaction contract

Register secrets as literal values or supply the environment entries that contain them. The environment policy uses credential-like names and explicitly configured additional names. Use a mask that contains no secret values.

The handler masks the whole value of any Authorization, Proxy-Authorization, Cookie, Set-Cookie, Cookie2, or Set-Cookie2 field. Name matching is exact and case insensitive at every attribute and decoded JSON depth. A structured scalar becomes a mask string when it matches a registered value or cannot be encoded as JSON. The redactor checks string leaves and rendered representations of other scalars, including duration nanoseconds and JSON representations of numbers and times. Nonmatching scalars that can be encoded keep their type. The handler drops an attribute if flattening its group path or generating a reserved-key alias would expose a registered value.

An input accepted by Go’s JSON validator is decoded before redaction and emitted as valid JSON. This includes JSON escapes in names and URL values. A record can keep its original bytes when every decoded key and value survives redaction and no keys are repeated. When a record needs rewriting, object order, whitespace, duplicate keys, and the precise text around secrets can change. A noncredential key remains independent of a similarly named credential key. Registered values can also mask keys or match an escaped Go string representation. If a registered literal spans JSON syntax or cannot be accounted for by a decoded scalar, the redactor replaces the document with a JSON mask string. A JSON-looking string inside a JSON value is also checked.

For unstructured text, the redactor masks registered values and uses two conservative patterns. A hierarchical URL with a literal scheme separator can mask everything between that separator and the last `@` on the same line. An Authorization or Proxy-Authorization assignment masks its value to the end of that line. A line ends at LF or CR. Pass other credential fields as structured attributes. Incomplete URLs without an `@` require a registered value or a structured credential field.

Use `RedactDiagnostic` for a complete diagnostic that may contain folded Authorization values. It masks nonblank indented continuation lines and a Basic or Bearer value on the line immediately after an empty Authorization assignment. A blank line ends the value. When the input is valid JSON, it also checks folded values inside decoded string fields and emits valid JSON.

Use `RedactRegistered` to mask only registered values, with no text patterns and no JSON parsing. It matches each value as the raw bytes that were registered and as its JSON and Go-quoted escaped forms, across record separators, and overlapping or adjacent matches share one mask. Match tracking uses space proportional to the number of separate matches within the longest registered form's window, plus a fixed allowance. A caller can run it on a complete buffer before any record splitting.

`RedactDiagnostic` limits URL masking to the authority. A slash, query, fragment, whitespace, double quote, or angle bracket ends the authority, so a later `@` in the path or another field leaves the host intact. Register credentials that appear in malformed URLs outside the recognized authority. An ambiguous authority containing a raw backslash may still be masked through a later `@`.

`NewStream` buffers each diagnostic record through LF, CR, or `Flush`. It checks a record as JSON when the record is valid JSON on its own. Records longer than 16 KiB are replaced with one JSON mask string, and processing resumes at the next delimiter. A registered value spanning records replaces each affected record with a JSON mask string and retains its delimiter. Record separators are kept as framing, including CRLF. A registered value consisting solely of separators does not remove them. The longest possible registered-value prefix carries at most the longest registered value minus one bytes, in addition to the bounded record buffer. Call `Flush` when the diagnostic stream ends. A stream with a nil redactor passes bytes through.

Use `NewDiagnosticStream` to preserve long plain diagnostic tails for a bounded capture. It can emit parts of a record longer than 16 KiB when its first nonblank byte is outside the set of possible JSON starting bytes and its first 16 KiB contain neither `:` nor `=`. It retains possible registered-value prefixes across writes. If a `:` or `=` later appears, the stream masks the unresolved suffix through the next record delimiter. Long sequences of overlapping matches can emit multiple masks so that output continues during the record. If the mask includes a record separator, or a registered value contains a record separator or a byte present in the mask, the stream uses the conservative record buffering described above. `RedactDiagnostic` uses the same overlapping-match behavior for a complete long plain record.

The diagnostic stream masks nonblank indented Authorization continuations and recognizes Basic or Bearer immediately after an empty assignment. It preserves the original delimiters and checks decoded strings in complete JSON records. An oversized opaque record can suppress a following possible continuation as a precaution.

Use a separate channel for interactive prompts, protocol stdout, or raw business data. Pretty-printed JSON requires a complete input. The redactor does not decode other secret representations such as base64, hex, or percent encoding. Register the emitted representation or supply a parsed structured field. Never log secrets intentionally.

## Desktop diagnostic scanner

This module provides `desktop/diagnostic-redaction.ts` to identify credential
assignments in diagnostic text. Desktop will adopt the scanner in a follow-up
change. The scanner returns ordered, half-open UTF-16 ranges. Consumers choose
the replacement marker and apply their separate registered-value policy.

Text assignment labels use ASCII letters, digits, dots, underscores, and
hyphens. Quoted labels and recognized bare compound names such as `private key`
can also contain spaces and tabs. For classification, the scanner lowercases
the label and removes separators. It recognizes labels containing `password`, `passwd`,
`passphrase`, `token`, `secret`, `apikey`, `authorization`, `credential`,
`privatekey`, `accesskey`, or `telemetrydistinctid` after normalization.
`Authorization` and `Proxy-Authorization` names, including terminal
dot-qualified properties, use authorization value rules. Unquoted values
include punctuation up to whitespace. Quoted and grouped values keep their
complete framing. Incomplete framing or an ambiguous authorization tail masks
through the end of the input. A separated terminal `workspace=<identifier>`
field remains visible only after a complete authorization value on the same
physical line.

Consumers must register unlabeled credentials, values under other names such
as `Cookie` or `pwd`, and credentials whose labels fall outside this syntax.
The scanner does not inspect environment variables, decode arbitrary encodings,
or modify raw protocol, terminal, or application data.

Run `node --test logging/desktop/*.test.ts` from the repository root with
Node.js 24 or newer. Local development uses Node.js 24 through mise and CI
uses Node.js 26. No TypeScript flags are required.
