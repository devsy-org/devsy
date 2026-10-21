# Issue #1621 verification and release coordination

This evidence concerns the SSH provider plus core agent injection. It does not qualify every provider. No release, merge, or managed activation is authorized by this document.

Fixture snapshots below retain their original artifact revisions and scope. Later provider PR #152 CI on `e9c4c75e81853bcb11cf707d4aff9a49bb739411` passed all 18 Linux/Bash workspace integration cases with released core v1.24.0, five-platform snapshot packaging, and native Windows stdin tests. This adds bounded whole-CLI and Windows-runner evidence; it does not establish Desktop acceptance, whole-workspace coverage across every login shell, or published artifacts.

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

Repeated Ubuntu 26.04 CLI snapshot builds exhausted runner disk quota while compiling or linking concurrent Linux architectures. The Linux build job now serializes those same two targets and allows 45 minutes for cold serial execution and follow-on steps. Read-only disk/cache diagnostics surround the snapshot. No target, test, action pin, permission, cache, or unrelated runner file is removed; macOS and Windows build concurrency is unchanged. Final-head GitHub execution remains the resource-fix gate.

Working core base: `d3f6e421642575c5072badcf56bf5b0a5606bd41`. Final script/source SHA-256:

| Input | SHA-256 |
| --- | --- |
| Original v1.24.0 `inject.sh` | `c2effffd6bbb9e42146bce22e2393f55f19b88d7b152f59d09c3c1e749bc2e0b` |
| Pre-Dockerless-fix `pkg/inject/inject.sh` | `b07ab67954708ee0bffac3b76a498374833a0383225bbf8acedf9e71c46d44c3` |
| Pre-permissions-fix Dockerless-compatible `pkg/inject/inject.sh` | `61d88ef1fd4394f35026d2c57cf3a3e29562385e2e8799a35a7f2fb097090294` |
| Readable staged-agent `pkg/inject/inject.sh` | `1c2b70523285dcca664a61180e5e56fccd09af4ef08a005cdf9ff2d36e62dcdd` |
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

## Dockerless installer compatibility

Core E2E on `16532a90792a78839335ffb066f95f1163776da4` exposed a separate container boundary. Kubernetes native staging failed first with a broken stdin stream/WebSocket EOF. Its cleanup call supplied no streams, which the Kubernetes API rejects. Those driver/delivery implementations are unchanged from the baseline; the initiating stream failure remains unproven. The subsequent legacy fallback exposed a confirmed regression: the Dockerless bootstrap filesystem has `/.dockerless/bin/sh` but no `/bin/sh`.

Ordinary disposable execution of `ghcr.io/devsy-org/dockerless@sha256:8910f52719e38ffe986652efa04796d781575797627cdba3dd1ba8f977078303` confirmed both image variants use a static BusyBox shell at that bundled path. The ARM64 manifest is `sha256:1c1e4242bfe3662fe6531f52833b185c1cefc6e0ff01aa4279df3edd3a359663`; AMD64 is `sha256:58621764ce6d3921b68b05b7fb9fdca7250fc17f906e919961ba8725d87c7fd4`. AMD64 executed through existing emulation on the ARM64 host, rather than native AMD64 hardware.

The immutable pre-fix bootstrap (`b07ab67954708ee0bffac3b76a498374833a0383225bbf8acedf9e71c46d44c3`) failed after `ping`, without requesting a payload. Original v1.24.0 injection succeeded in the same ARM64 image with a synthetic ELF payload and live follow-on. The corrected bootstrap selects the fixed bundled interpreter only when `DOCKERLESS=true`, `/bin/sh` is not executable, and the bundled interpreter is executable. Ordinary targets retain `/bin/sh`, which is preferred even in Dockerless containers when available. Missing prerequisites fail before permission probes or escalation; warm reuse does not require an installer.

The corrected script (`61d88ef1fd4394f35026d2c57cf3a3e29562385e2e8799a35a7f2fb097090294`) passed six ordinary-image cold/warm/forced synthetic cells across both architectures, including hostile literal paths and exact 4 KiB plus live follow-on bytes. Four negative cells (disabled Dockerless or both interpreters missing, each architecture) failed explicitly without requesting payloads. The source-exact native staging command and cleanup separately passed on both architectures. These process tests do not reproduce Dockerless `start --wait`, its filesystem transition, or Kubernetes exec/WebSocket transport, and do not establish the initiating native failure's cause. No builder, privileged container, host mount, or security override was used.

The actual bundled BusyBox `su` wrapper also preserved empty and hostile literal arguments byte-for-byte and live stdin on both architectures, using the existing root account from a root process. This verifies shell/argument compatibility, not non-root privilege escalation. Both diagnostic containers were removed after validation.

Persistent `TestPrivilegedInstallerModesLiveStream` and `TestPrivilegedInstallerFailureStatus` exercise the real dispatcher in sudo/su mode using strict test doubles. The doubles validate invocation flags and pass the requested code unchanged to a real POSIX shell; markers stay off protocol stdout. Actual `Inject` sequencing checks hostile paths/URLs, exact binary and live NUL-containing bytes, one `done`, and failure status 37. These deterministic tests require no host privilege operation and complement, rather than replace, the real-tool evidence above.

Follow-ups remain precise: investigate Kubernetes staging diagnostics/stream closure in the Dockerless filesystem transition, and provide an explicit diagnostic stream for staged-file cleanup rather than submitting an all-false exec stream request. This change preserves native argv routing and configured interactive shells. SSH provider bootstrapping still requires `/bin/sh` before the core script executes.

## Elevated installation permissions

CodeRabbit identified a staging regression: `mktemp` creates mode `0600`, so adding only executable bits published a root-owned `0711` agent after sudo installation. A non-root user could execute that file but could not reopen its own executable. Publication now sets the staged agent to `0755` before the atomic rename. The chmod-failure path still retains the prior agent and removes only staging files. Persistent direct, sudo/su-dispatcher, and Dockerless-selection tests assert the final mode; failure cases retain the original mode.

An ordinary disposable Linux ARM64 container reproduced the mechanism with the actual `GenerateScript` and `Inject` APIs, a non-root uid 1001, and real noninteractive sudo into an unwritable root-owned destination. The immutable `61d88ef1...` script published root-owned `0711`; the native synthetic ELF started but failed `os.Open(os.Executable())` with permission denied and exit 42. With script `1c2b7052...`, the identical 2,403,587-byte ELF was root-owned `0755`, reopened and read itself, and completed an exact 4,124-byte binary/live exchange with empty stderr and no error. Both runs requested one payload. The rendered scripts differed only in `chmod -- +x` versus `chmod -- 0755`.

The synthetic ELF SHA-256 was `8f3d322e43d67c91d924de36054d2a66702720da0ea109242c89595b85af77d3`; successful stdout SHA-256 was `445e35c83283238eb1b2ef4bfcdc4e6a53f9600c7f99136a662e8304d6298a12`. This is an actual permissions and streaming reproduction, not a new full Devsy-agent or whole-product qualification. Ubuntu uutils 0.10.0, GNU coreutils 9.4, and BusyBox 1.38 accepted the fixed chmod syntax. The container, its internal test user and sudo policy were removed; no host accounts or security settings changed.

The fixed matrix validator now explicitly rejects failed child execution, missing or false execution/duplex indicators, nonempty injection/process errors or stderr, and incorrect payload request counts (cold/replacement one, warm zero). Evidence is written before failure, and host-owned exit/phase metadata cannot be overwritten by child JSON. Controlled validator-only subprocess tests under `python3 -O` verified both failure and baseline diagnostic behavior; they are not runtime-matrix evidence. The retained historical 30-cell result also passed the stricter gate.

## Matrix child-failure evidence

A later CodeRabbit review identified that a timed-out or malformed child result could stop the matrix before its evidence file was written. A controlled baseline reproduction confirmed that a successful row followed by `TimeoutExpired` escaped with no output file. The helper now records timeouts, failed child startup, invalid/non-object JSON, and missing or wrongly typed output fields as failed cells, retaining partial output and separate child-process stderr. The fixed gate rejects those cells after the complete evidence file is written; baseline runs remain diagnostic. Host-owned exit and diagnostic fields cannot be forged by child JSON.

Run the persistent helper regressions with `python3 -O -m unittest discover -s docs/issue-1621 -p 'test_matrix.py'`. They simulate child failures in both baseline and fixed entry points, check that earlier and later rows survive, and execute a real local subprocess to verify nonzero exit and stderr override forged JSON metadata. They do not start SSH, containers, or the product and do not replace runtime qualification. Product-source hashes are unchanged by this helper-only follow-up.

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
