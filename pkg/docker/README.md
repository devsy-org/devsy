# Docker command output

`Streams.Stdout` and `Streams.Stderr` are synchronous capture destinations. Non-file live destinations use bounded delivery and are redacted before the live queue.

A live `*os.File` without a capture destination is passed directly to the Docker process. This preserves terminal detection and resizing, but the helper does not redact that output. A live `*os.File` combined with capture is written through a synchronous `io.MultiWriter`. The Docker process sees a pipe rather than a terminal, so a TTY cannot be combined with capture. A blocked file in this combination can also block command return.

When a destination is used for both capture and live output, it is kept on the synchronous capture side. A blocked capture destination can prevent command return.
