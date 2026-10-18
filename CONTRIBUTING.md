# Contributing to Devsy

Thank you for your interest in contributing to Devsy! This guide will help you get started with development.

## Contributor License Agreement

Before your first contribution can be merged, you must sign the [Contributor
License Agreement](./.github/CLA.md). When you open a pull request, a bot will
check your signing status and, if needed, comment with instructions. You sign by
posting a comment on the pull request with the exact text the bot provides.

## Development Setup

### Prerequisites

**CLI Development:**

- [Go 1.26+](https://go.dev/doc/install)
- [mise](https://mise.jdx.dev/) (installs the pinned toolchain and runs project tasks via `mise run <task>`)

**Desktop Application Development:**

- [Node.js 24+](https://nodejs.org/en/) (with npm)
- [Go 1.26+](https://go.dev/doc/install)
- [Python 3.12+](https://www.python.org/) (for native module builds)

### Initial Setup

1. Clone the repository:
   ```bash
   git clone https://github.com/devsy-org/devsy.git
   cd devsy
   ```

2. If you want to change Devsy agent code:
   - Exchange the URL in [DefaultAgentDownloadURL](./pkg/config/repo.go) with a custom public repository release
   - Build devsy via: `mise run cli:build:dev`
   - Upload `dist/devsy-dev_linux_amd64_v1/devsy-linux-amd64` and ARM64 variant to your public repository release assets

## Building from Source

### CLI

**Using mise (recommended):**

```bash
# Build CLI for development
mise run cli:build:dev

# Build CLI for production
mise run cli:build

# Build with Pro features
mise run cli:build:dev:pro
```

**Using Go directly:**

```bash
CGO_ENABLED=0 go build -ldflags "-s -w" -o devsy
```

The binary will be output as `devsy` in the current directory.

### Desktop Application

**Using mise (recommended):**

```bash
# Setup Electron environment (first time only)
mise run desktop:setup

# Run in development mode
mise run desktop:dev

# Build the application
mise run desktop:build
```

**Manual build:**

```bash
cd desktop
npm ci
npm run build
```

The packaged application will be in `desktop/release/`

## Development Workflow

### CLI Development

```bash
# Tidy dependencies
mise run cli:tidy

# Run linters
mise run cli:lint

# Run unit tests
mise run cli:test

# Build for development
mise run cli:build:dev
```

### Desktop Development

In renderer code, use `$lib/...` for imports across renderer library modules and
`$shared/...` for shared modules. Apply this to type imports, re-exports, and
dynamic imports as well. Keep `./...` for sibling files and relative paths for
renderer entry points without an existing alias. Avoid parent-directory imports
(`../...`) when one of these aliases addresses the module.

Main-process, preload, and shared code use relative imports: the renderer aliases
are not configured in the main/preload bundlers. Preserve the existing `.js`
extension on TypeScript module specifiers and `.svelte` on component imports.

```bash
# Install dependencies
cd desktop
npm ci

# Check code quality (type check + svelte-check)
mise run desktop:check

# Run unit tests
mise run desktop:test

# Run e2e tests
mise run desktop:test:e2e
```

### E2E Testing

```bash
# Build for e2e tests
mise run cli:test:e2e:build

# Run all e2e tests
mise run cli:test:e2e

# Run specific test suite
mise run cli:test:e2e:suite "suite-name"

# Run focused tests
mise run cli:test:e2e:focus "test-pattern"

# Setup kind cluster for testing
mise run cli:test:e2e:kind:setup

# Teardown kind cluster
mise run cli:test:e2e:kind:teardown
```

### gRPC Development

If you need to modify the gRPC tunnel code:

```bash
mise run cli:build:grpc
```

This requires:
- `protobuf-compiler` (install via `sudo apt install protobuf-compiler`)
- Go protobuf plugins (installed automatically by the task)

## Testing Your Changes

### Quick Start

1. Build Devsy:
   ```bash
   mise run cli:build:dev
   ```

2. Add a provider:
   ```bash
   ./dist/devsy-dev_linux_amd64_v1/devsy-linux-amd64 provider add docker
   ```

3. Configure the provider:
   ```bash
   ./dist/devsy-dev_linux_amd64_v1/devsy-linux-amd64 provider use docker
   ```

4. Start a workspace:
   ```bash
   ./dist/devsy-dev_linux_amd64_v1/devsy-linux-amd64 workspace up github.com/microsoft/vscode-remote-try-node
   ```

### Using Act for Local CI Testing

```bash
# Build UI using act
mise run desktop:act:build:ui

# Build desktop app using act
mise run desktop:act:build:app

# Build flatpak using act
mise run desktop:act:build:flatpak

# Run e2e tests with focus
mise run cli:test:e2e:act:focus "test-pattern"
```

## Developing Providers

Read [the docs](https://devsy.sh/docs/developing-providers/quickstart) for an introduction to developing your own providers.

### Publishing Your Provider

Once your provider is ready:

1. Update `community.yaml` with your provider information
2. Update `sites/docs-devsy-sh/content/docs/managing-providers/manage-providers.mdx` with documentation

This will feature your provider in both the documentation and the UI.

## Desktop Deep Links

Devsy Desktop can handle deep links to perform various actions.

**URL Scheme:**

```
devsy://command?param1=value1&param2=value2
```

### Open Workspace

Open a workspace based on a source (similar to `devsy workspace up`, but shareable):

```
devsy://open?source=<url-encoded-source>&workspace=<name>&provider=<provider>&ide=<ide>
```

**Parameters:**

- `source` (required): URL-encoded workspace source
- `workspace` (optional): Workspace name
- `provider` (optional): Provider to use
- `ide` (optional): IDE to open

**Example:**

```
devsy://open?source=https%3A%2F%2Fgithub.com%2Fuser%2Frepo&workspace=my-workspace&provider=docker&ide=vscode
```

### Import Workspace

Import a remote Devsy.Pro workspace into your local client:

```
devsy://import?workspace_id=<id>&workspace_uid=<uid>&devsy_pro_host=<host>&options=<options>
```

**Parameters:**
- `workspace_id` (required): Workspace ID
- `workspace_uid` (required): Workspace UID
- `devsy_pro_host` (required): Devsy Pro host URL
- `options` (optional): Additional options

## Repository Structure

- `/cmd` - CLI command implementations
- `/pkg` - Core packages and libraries
- `/desktop` - Desktop application (Electron + Svelte)
- `/e2e` - End-to-end tests
- `/providers` - Built-in provider definitions
- `/sites` - Documentation and download websites
- `/hack` - Build and development scripts

## Pull Requests

The pull request template asks for three things reviewers rely on:

- **AI disclosure.** AI-assisted code is welcome. State the tool, model, and what it was used for, and make sure you understand every line you submit.
- **Integration tests.** CLI behavior changes need Ginkgo e2e coverage under `e2e/tests/`. Desktop behavior changes need Playwright e2e coverage under `desktop/e2e/`. Unit tests alone are not enough. If you believe no integration test is needed, say why in the PR.
- **Conventional Commit title.** The title becomes the squash commit subject.

## Getting Help

- [Documentation](https://devsy.sh/docs)
- [GitHub Issues](https://github.com/devsy-org/devsy/issues)
- [GitHub Discussions](https://github.com/devsy-org/devsy/discussions)

## Code of Conduct

Please be [respectful and constructive](https://mikemcquaid.com/open-source-maintainers-owe-you-nothing/) in all interactions with the community.
