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

The handler masks the whole value of any Authorization, Proxy-Authorization, Cookie, Set-Cookie, Cookie2, or Set-Cookie2 field. Name matching is exact and case insensitive at every attribute and decoded JSON depth. A structured scalar becomes a mask string when it matches a registered value. The redactor checks string leaves and rendered representations of other scalars, including duration nanoseconds and JSON representations of numbers and times. Nonmatching scalars keep their type.

An input accepted by Go’s JSON validator is decoded before redaction and emitted as valid JSON. This includes JSON escapes in names and URL values. Object order, whitespace, duplicate keys, and the precise text around secrets can change. A noncredential key remains independent of a similarly named credential key. Registered values can also mask keys or match an escaped Go string representation. If a registered literal spans JSON syntax or cannot be accounted for by a decoded scalar, the redactor replaces the document with a JSON mask string. A JSON-looking string inside a JSON value is also checked.

For unstructured text, the redactor masks registered values and uses two conservative patterns. A hierarchical URL with a literal scheme separator can mask everything between that separator and the last `@` on the same line. An Authorization or Proxy-Authorization assignment masks its value to the end of that line. A line ends at LF or CR. Pass parsed headers as structured attributes if they contain other credential fields or folded values. Incomplete URLs without an `@` require a registered value or a structured credential field.

`NewStream` buffers each diagnostic record through LF, CR, or `Flush`. It checks a record as JSON when the record is valid JSON on its own. Records longer than 16 KiB are replaced with one JSON mask string, and processing resumes at the next delimiter. A registered value spanning records replaces each affected record with a JSON mask string and retains its delimiter. Record separators are kept as framing, including CRLF. A registered value consisting solely of separators does not remove them. The longest possible registered-value prefix carries at most the longest registered value minus one bytes, in addition to the bounded record buffer. Call `Flush` when the diagnostic stream ends. A stream with a nil redactor passes bytes through.

The stream does not guarantee early output of an unterminated fragment or a particular output size after redaction. Use a separate channel for interactive prompts, protocol stdout, or raw business data. Pretty-printed JSON requires a complete input passed to `Redact`. The redactor does not decode other secret representations such as base64, hex, or percent encoding and does not interpret folded headers in unstructured text. Register the emitted representation or supply a parsed structured field. Never log secrets intentionally.
