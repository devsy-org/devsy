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

## Protect diagnostic data

The redactor masks supplied secret values, values from credential-like environment names, URL userinfo in `scheme://` authorities, and Authorization header credentials. It recognizes Authorization, Proxy-Authorization, Cookie, and Set-Cookie fields in diagnostic text and masks their values across chunk boundaries. It also processes diagnostic messages, keys, groups, errors, numeric values, and standard base64-encoded JSON byte values. Supply secrets the environment policy cannot identify. A nil handler redactor still protects recognized credential formats. Redaction cannot discover every secret. Avoid logging sensitive input and retain host-side protection where needed.

`redact.NewStream` supports chunked diagnostic streams. Call `RedactChunk` for each chunk and `Flush` at the end. An unresolved URL authority has a 512-byte lookahead including any carried scheme bytes. Userinfo extends to the last `@` before the authority ends. When it remains ambiguous beyond the lookahead, the stream masks the authority and keeps at most 512 bytes from a possible final `@`. Resolution at a slash, double quote, backslash, `?`, `#`, whitespace, or `Flush` can recover the final host suffix. This can mask an overlong ordinary authority split across calls. A complete ordinary URL remains unchanged when its delimiter makes it unambiguous in the supplied input. At a closing double quote or stream finalization, an unresolved authority with a colon and an invalid host or port is masked. Enclosing diagnostic punctuation is preserved.

Known secrets across chunks use an append-backed pending buffer. Long unresolved match churn can defer safe output until later input or `Flush`. Complete unambiguous diagnostics are emitted immediately. There is no wall-clock emission bound. After each `RedactChunk`, pending byte length is bounded by `max(N + floor(N/2), max(N, 512) + 9)`, where `N` is the longest possible proper prefix of a known secret, or zero if there are none. Do not use this stream to rewrite protocol or raw business output.

Explicit Authorization and Proxy-Authorization fields preserve a recognized Basic or Bearer scheme and mask the credential payload. Other schemes and explicit Cookie or Set-Cookie fields are masked in full. For serialized text, the tokenizer uses the quoted value, container, logfmt token, or HTTP header line as the value boundary. It recognizes JSON escapes in field names and consumes folded header lines. An escaped diagnostic field without reliable raw delimiters is masked through the diagnostic line, including folded continuations. The handler also masks structured objects and groups when their field names indicate credentials. Once an unquoted header line starts, punctuation and spaces belong to its value until the line ends.
