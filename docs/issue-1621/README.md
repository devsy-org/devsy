# Issue #1621 verification and release coordination

This evidence concerns the SSH provider plus core agent injection. It does not qualify every provider. No release, merge, or managed activation is authorized by this document.

## Actual Linux fixture

The 2026-10-20 run used disposable Ubuntu 26.04 aarch64 containers on a macOS host, OpenSSH 10.2p1, Bash 5.3.9, Zsh 5.9, Fish 4.2.1, Dash `/bin/sh`, and BusyBox 1.37.0. Five distinct non-root passwd entries used `/bin/{bash,zsh,fish,dash,ash}`. Password authentication, PAM, root login, and MOTD were disabled; public-key authentication used fresh temporary keys. Only an ephemeral localhost port was published. No host accounts, servers, shell configuration, or SSH keys were changed.

Independent clones tested BusyBox `/bin/sh`, missing `/bin/sh`, and non-executable `/bin/sh`; the shared server retained Dash. Runtime results and credentials remained outside the repository. The fixture image actually used was the cached `mcr.microsoft.com/devcontainers/base@sha256:91eff046d5c12318fc98e59258982b87ba896c467427ed8b5dab5a02107a5db7`. The helper defaults to the cached GHCR base and never pulls images; OS packages are installed inside the disposable container.

## Baseline and corrected transport

Original core v1.24.0 (`1f47cd185bd134b45cdce12b947f6c601d76d329`) and original provider v1.1.44 (`e37a6c68a695d9d1d3806b2d42a755750816f60e`) were independently exported with `git archive` and built from immutable temporary sources. Working-tree implementation edits did not enter the baseline builds.

| Actual passwd shell | Original external SSH | Original built-in SSH |
| --- | --- | --- |
| Bash | Success: `ping`, `ARM-true`, `done`, payload output | Same success |
| Zsh | Exit 127; `main:7: command not found: sh -c`; raw stdout only `ping\n` | Exit 127; raw `ping\n`; remote diagnostic lost |
| Fish | Exit 127; unsupported assignment syntax; no stdout | Exit 127; no stdout; diagnostic lost |

For successful baseline injection the transferred synthetic executable printed `agent-started\ntunnel-after-handshake\n`. Baseline failure surfaced as `read inject signal: EOF` for Zsh and `read ping: EOF` for Fish. Baseline used original raw injection commands, without a fixture-side POSIX wrapper.

The corrected matrix used the actual provider worker's executable, not a substitute SSH launcher, plus the real core `GenerateScript` and `Inject` APIs. A synthetic Python executable distinguished protocol/byte verification from real Devsy startup.

- Five passwd shells × external/built-in SSH × cold/warm/forced replacement: **30/30 passed**. Each returned exactly 4,110 bytes: 4 KiB containing all byte values, `READY\n`, then `LIVE-OK\n`. The host sent the follow-on request only after receiving output. SHA-256: `7085d9188f2d7d361c78e10a5075b746a0004d17a740085b0363dc49c5c14b43`.
- Final `chmod -- +x` core rerun: **30/30 passed** with random 4 KiB including NUL, quotes, dollar, backtick, CR, newline and backslash; hostile literal destination paths in every cell. Exact output SHA-256: `4d77b6416f4dfdf3a243a3cc27c79dced201dc7b01462e104b1b0d53af6ae9a4`.
- Cold/replacement requested one payload; warm requested none. All successful normal cells had empty stderr and error, with raw `ping\nARM-true\ndone\n` or warm `ping\ndone\n` before payload bytes.
- Unchanged original v1.24.0 core with corrected provider: **6/6 passed**, Bash/Zsh/Fish × both transports, random 4 KiB and genuinely live follow-on. This was original core injection with a synthetic executable, not a rebuilt v1.24.0 full agent.
- Provider init: **10/10 passed**, five shells × both transports; writable per-user agent path and `/bin/true` Docker stand-in. This verifies init transport/output checks, not a real remote Docker daemon.

## Actual Linux failures and recovery

| Case | Observed result |
| --- | --- |
| BusyBox `/bin/sh` clone, both transports | Exact binary/live exchange passed |
| Missing `/bin/sh`, both transports | Exit 127, diagnostic preserved, no `ping`, no payload request |
| Denied `/bin/sh`, both transports | Exit 126, diagnostic preserved, no `ping`, no payload request |
| Remote stdout/stderr and exit 37, both transports | Exact `ordinary-out`, exact `remote-diagnostic`, exit 37 preserved |
| Sudo denied/restrictive `/root` destination, both transports | Error explicitly requires passwordless sudo; only `ping`; no payload request |
| Real sudo installer, native synthetic ELF, both transports | Root-owned unwritable destination installed successfully; normal non-root execution returned exact binary/live exchange |
| Download failure, then stdin, both transports | Three failed localhost downloads followed by successful exact binary/live exchange; test-local `sleep` shim bypassed retry waits |
| No curl/wget in invocation-local PATH, both transports | Clear diagnostic, then successful stdin binary/live exchange |
| Truncated frame plus no download tools, both transports | Exit 1; raw `ping\nARM-true\n`, no `done`; complete-receive and both-methods-failed diagnostics; pre-existing executable still prints `old-valid` |
| Noisy Zsh startup in isolated clone, both transports | Noise remains visible before command output; core built-in test rejects it with `unexpected start line "noisy-rc"`, executed=false, zero payload requests |

An independent read-only reviewer ran the actual `quote_installer_argument`/`run_installer_script` functions with Linux util-linux 2.41.3 and BusyBox 1.37.0 `su -s /bin/sh -c ... root`. Both preserved empty strings, apostrophes, backslashes, Unicode, CR/newlines, substitutions, leading dash and trailing newlines as exact NUL-delimited arguments. Both preserved `abcdeLIVE\n` across `head -c 5; printf '|'; cat`, producing `abcde|LIVE\n`. GNU and BusyBox accepted `chmod -- +x`.

## Real agent and remaining acceptance

The actual development Linux aarch64 Devsy binary (217,260,362 bytes) was transferred and started as `internal ssh-server --stdio`. Initial Zsh external and Bash cold/warm/replacement tunnels completed `SSH-2.0-Go` and two separate command sessions, returning `first-command` and `live-followon`, without stderr. The real-agent startup matrix passed **10/10**: all five passwd shells × both transports, each with SSH handshake and two sequential commands. Six cells reused an installed executable; Dash/ash × both transports performed cold transfers. Earlier Bash/Zsh runs additionally exercised cold/reuse/forced replacement. Some initial cold development-binary transfers exceeded the original 30-second injection/40-second client bounds; those attempts are recorded harness-limit failures, superseded by the successful 150-second-bound startup matrix. The reusable tunnel harness allows 150 seconds for large development binaries.

Whole CLI `--ide=none` workspace acceptance: **BLOCKED**. Automatic approval review rejected the proposed privileged Docker-daemon fixture because it broadens the host boundary for container workloads; that action was not delivered or run. A safer capability check in the existing fixture as non-root `fixture-zsh` returned `unshare: Operation not permitted` (exit 1). No Docker socket, Docker client/daemon, or rootlesskit was present. No privileged container, host socket mount, seccomp relaxation, or indirect workaround was attempted. The SSH-only fixture uses `/bin/true` during provider init and does not establish the whole CLI workspace flow. Desktop acceptance: **BLOCKED**; no configured Desktop session/workspace fixture was provided. Native macOS unit/static checks are separate from the actual Linux/OpenSSH results above. Original v1.24.0 full-agent startup is **NOT RUN**; original core injection compatibility is verified above.

Earlier provider artifact (`final5`) SHA-256: `7e18087de9fbc9137a7a561c6fce200b7fe50d523fd99db08b6e8ce7b27015c7`. The complete random/hostile 30-cell synthetic matrix was rerun against that artifact and passed; exact output SHA-256: `475f5b02c861d431216e56c8f7ccd56e5fada5e0e25c1c7fce5172dcefd5f349`. Earlier transport matrix and real cold transfers used the provider worker's first corrected artifact SHA-256 `b7e79349ee68e8685f03ba98ea03427dcb8cd02a31bea54648a902c1dc322c22`; final5 adds stricter initialization and stdin/context shutdown. A final5 rerun also passed all ten real-agent startup/two-session cells using the installed actual Devsy binary. The staged provider diff digest supplied by its owner was `5d9b03da0b349f36b2f3b539e29a454dd1490afa63def9bec145119e167d51b4`; this is a working-diff identifier, not a release SHA.

The post-review provider artifact SHA-256 is `39468e46550ddf4a9ea3a1033cae10ef02d0feabcce42ad7b89a7f88f40ba989`, with production-diff fingerprint `fde9ee3eceaba52ab03246f0d59151c7070c967168edbce6b63a04ec1ddef1a0` supplied by its owner. This artifact passed a fresh **30/30** random/hostile synthetic matrix, **10/10** real-agent warm startup/two-session matrix, **6/6** unchanged-v1.24.0-core compatibility matrix, and both actual-agent SIGTERM checks (external 0.46 s, built-in 0.41 s; no local children or remote agent remaining). Final reviewed init and exact stdout/stderr/exit-37 checks passed in all ten shell/transport cells (20 checks). The final random-matrix exact stdout SHA-256 is `b8f99178b99fa86953567a3672e32a7427d140ef372cf563c3d296ad8b495ca2`. The final warm fixture was restored from the same cached base and prepopulated through Docker copy with the identical agent artifact; these are warm-start results, not new cold-transfer claims. The post-review changes address platform-specific redirected stdin and input-copy errors. Windows tests were cross-compiled locally; native Windows execution remains **NOT RUN**.

Native Linux execution of the actual `TestBootstrapSuite` and unchanged `TestInjectScript_RealShell*` tests passed with `/bin/sh` pointing at BusyBox. Cross-compilation only produced the test executable; these results came from executing it in the Linux clone.

The first GitHub Ubuntu BusyBox step exposed a test-double portability issue: Ubuntu's BusyBox static standalone applets bypassed PATH-based `sleep` and `chmod` doubles. The original failures were reproduced in a disposable Linux ARM64 container using Ubuntu Noble `busybox-static` `1:1.36.1-6ubuntu3.1`, the same package version as CI. Test-only shell functions now inject the faults in the outer and nested installer shells; successful binary/live-stream tests still execute the unmodified generated script. The chmod fault must emit its marker and the installer error, while download fallback must follow three failed attempts. The corrected bootstrap suite and unchanged real-shell transfer tests passed natively with that package as `/bin/sh`. No production installer or CI assertion was relaxed; final-head GitHub AMD64 execution remains a separate gate.

A later Ubuntu race run exposed an existing delivery-test observer race: stopping its pending `context.AfterFunc` could discard notification of a real attempt cancellation. The fixture now records the actual context cause after the runtime joins. The expected timeout causes, fresh-source retries, exact source close counts, bounded completion, and uncanceled outer-context assertions remain intact. Production delivery behavior is unchanged.

The macOS hostile-path fixture previously pipelined tunnel bytes immediately after the binary, before `done`. Independent fragmented-write reproductions showed BSD `head` reading ahead into those premature bytes. Production `Inject` waits for `done` before bridging user stdin. The fixture now exercises that actual API and sequencing, retaining exact binary, URL, raw protocol, live NUL-containing payload, and substitution-sentinel assertions. The existing real-shell transfer test remains unchanged.

Working core base: `d3f6e421642575c5072badcf56bf5b0a5606bd41`. Final script/source SHA-256:

| Input | SHA-256 |
| --- | --- |
| Original v1.24.0 `inject.sh` | `c2effffd6bbb9e42146bce22e2393f55f19b88d7b152f59d09c3c1e749bc2e0b` |
| Corrected `pkg/inject/inject.sh` | `b07ab67954708ee0bffac3b76a498374833a0383225bbf8acedf9e71c46d44c3` |
| `pkg/inject/script.go` | `752c82c5f3df1e60359658bc961c557b35995636dab88618ca8e791e21bf8e3e` |
| `pkg/agent/inject.go` | `3ea3bb44cbc37132bde6569b68e0425ea8c91d744c3a6ea94988d99d3e6c61e7` |
| Actual Linux agent executable | `5e5853e9e0653830858fe2438b46a7e8eb94a927bbbb4fda3142c2ddbe173bc4` |

SIGTERM cancellation of the final5 provider with blocked stdin completed in under 0.5 seconds on both transports after observing the actual agent SSH banner. Local SSH children/connections and the remote Devsy stdio agent terminated. The built-in command reports context cancellation and exits 1; external SSH exits 255. An arbitrary remote `exec sleep 1621` survived the closed non-PTY channel; those disposable probes were explicitly removed. Cancellation guarantees local transport/stdin-pump shutdown and observed agent cleanup, not remote arbitrary-process reaping or SIGKILL cleanup.

## Reproduction

Require a cached supported Ubuntu image, Docker, Python 3, OpenSSH client and the repository Go toolchain. Use a fresh temporary state directory and fresh container name. Never put state/keys/output under tracked docs.

```sh
python3 docs/issue-1621/ssh-fixture.py setup --state "$FIXTURE_STATE" --name "$FIXTURE_NAME"
python3 docs/issue-1621/ssh-fixture.py metadata --state "$FIXTURE_STATE" --name "$FIXTURE_NAME"
go build -o "$CORE_HARNESS" docs/issue-1621/inject-harness.go
go build -o "$TUNNEL_HARNESS" docs/issue-1621/real-tunnel.go
python3 docs/issue-1621/matrix.py --state "$FIXTURE_STATE" --core "$CORE_HARNESS" --provider "$PROVIDER_BINARY" --output "$RESULT_FILE" --hostile
PATH="$FIXTURE_STATE/bin:$PATH" "$TUNNEL_HARNESS" -provider "$PROVIDER_BINARY" -payload "$LINUX_AGENT" -port "$FIXTURE_PORT" -host fixture-zsh -builtin true -dest /home/fixture-zsh/real-agent
python3 docs/issue-1621/ssh-fixture.py teardown --state "$FIXTURE_STATE" --name "$FIXTURE_NAME"
```

`fixture.json` supplies the selected port and wrapper directory. The wrappers supply only the fixture SSH config, including the `ssh -G` lookup used by the built-in transport. Harnesses have `go:build ignore` and must be built explicitly. For baseline reproduction, export the original core into a fresh temporary directory, copy only `inject-harness.go` there, build it against that export's module, and run `matrix.py --baseline` with the original provider executable. Original-provider stderr contains its original timestamped logger.

The `forced-replacement` synthetic case explicitly sets the installation condition true; it is not a version-parser test. `real-tunnel.go -check` accepts the actual generated version check when testing version selection. Low-level framing failures can be reproduced with `inject-harness -script-out`, then passing `pong\n100\nabc` and EOF to the provider `COMMAND` while its invocation-local PATH omits curl/wget. Do not modify host binaries or accounts.

## T9 boundary audit and release coordination

Read-only inventory at base core `d3f6e421642575c5072badcf56bf5b0a5606bd41`:

| Boundary | Existing contract / required disposition |
| --- | --- |
| `pkg/devcontainer/config/userenvprobe.go` | Keep configured `GetShell` and `-lic`/`-lc`/`-ic` semantics. Probe intentionally evaluates the user's environment. |
| `pkg/ssh/server/ssh_container.go:getCommand`, `pkg/shell/shell.go:GetShell` | Keep passwd/configured shell for terminal commands and PTY login `-l`; do not force user terminals to POSIX shell. |
| `pkg/agent/delivery/factory.go` | Local Docker uses named-volume delivery; remote Docker uses native `docker cp`/`mkdir`/`chmod` argv. Available `ArgvExec` uses native driver calls; Apple and fallback custom drivers retain `LegacyShellDelivery`. |
| `pkg/agent/delivery/kubernetes.go` | Driver receives explicit argv, including deliberately owned `sh -c` scripts for staging/download/commit; do not wrap argv again as a remote login command. |
| `pkg/devcontainer/setup.go:execSetupSSHServer` | Native `ArgvExecDriver` starts the agent with literal argv; fallback `CommandDevContainer` string path carries raw protocol stdout. |
| Docker `CommandDevContainer` | Native CLI argv already selects `sh -c`; no remote login-shell wrapper needed. |
| Kubernetes `CommandDevContainer` | Root uses `sh -c`; non-root uses `su USER -c STRING`. Follow up separately on that user-shell contract if broadening scope. |
| Apple `CommandDevContainer` | Native container CLI argv explicitly selects `sh -c`; Apple delivery remains shell-based and requires its own actual macOS qualification. |
| Custom `CommandDevContainer` | Passes `DEVCONTAINER_COMMAND`/`DEVCONTAINER_USER` to configured provider command. Provider implementation owns final shell evaluation; audit each provider before making broader claims. |
| External runtime / microsandbox argv | Preserve raw literal argv and streaming; no login-shell wrapper should be added. |

Core and SSH-provider changes require independently versioned releases. Releasing only core does not repair Fish parsing in the old provider; corrected provider with old core passes this bounded compatibility matrix. Keep one canonical issue, cross-link both PRs and record exact released artifacts before claiming a shipped fix. Rollback must remain independent: revert/pin the provider separately from core, preserve a previously valid agent on failed replacement, and report which transport/version was restored. Release order, published-artifact qualification, full CLI/Desktop acceptance and merge remain pending owner decisions.
