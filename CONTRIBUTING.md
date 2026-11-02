# Contributing to Devsy

This guide covers setup, builds, tests, and pull requests.

## Contributor License Agreement

Sign the [Contributor License Agreement](./.github/CLA.md) before your first
contribution can merge. A bot checks your signing status when you open a pull
request. If you have not signed, the bot comments with instructions. Sign by
posting the exact text the bot gives as a comment on the pull request.

## Development setup

### Prerequisites

CLI development:

- [Go 1.26+](https://go.dev/doc/install)
- [mise](https://mise.jdx.dev/) (installs the pinned toolchain and runs tasks with `mise run <task>`)

Desktop application development:

- [Node.js 24+](https://nodejs.org/en/) (with npm)
- [Go 1.26+](https://go.dev/doc/install)
- [Python 3](https://www.python.org/) (node-gyp uses it to build the `node-pty` native module; CI uses 3.14)

### Initial setup

1. Clone the repository:
   ```bash
   git clone https://github.com/devsy-org/devsy.git
   cd devsy
   ```

2. To change the agent code:
   - Replace the URL in [DefaultAgentDownloadURL](./pkg/config/repo.go) with a public repository release that you control
   - Build Devsy: `mise run cli:build:dev`
   - Upload `dist/devsy-dev_linux_amd64_v1/devsy-linux-amd64` and the ARM64 variant to the release assets

## Build from source

### CLI

Using mise (recommended):

```bash
# Build CLI for development
mise run cli:build:dev

# Build CLI for production
mise run cli:build

# Build with Pro features
mise run cli:build:dev:pro
```

Using Go directly:

```bash
CGO_ENABLED=0 go build -ldflags "-s -w" -o devsy
```

The build writes the `devsy` binary to the current directory.

### Desktop Application

Using mise (recommended):

```bash
# Setup Electron environment (first time only)
mise run desktop:setup

# Run in development mode
mise run desktop:dev

# Build the application
mise run desktop:build
```

Manual build:

```bash
cd desktop
npm ci
npm run build
```

The packaged application is in `desktop/release/`.

## Development workflow

### CLI development

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

### Desktop development

In renderer code, use `$lib/...` for renderer library modules and `$shared/...`
for shared modules. This covers type imports, re-exports, and dynamic imports. Use
`./...` for sibling files. Use relative paths for renderer entry points that have
no alias. Avoid `../...` imports when an alias reaches the module.

Main-process, preload, and shared code use relative imports. Their bundlers do not
configure the renderer aliases. Keep the `.js` extension on TypeScript module
specifiers and the `.svelte` extension on component imports.

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

### E2E testing

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

### gRPC development

To change the gRPC tunnel code, run:

```bash
mise run cli:build:grpc
```

mise installs the pinned `protoc`. The task installs the Go protobuf plugins.

## Test your changes

### Quick start

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

### Local CI with act

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

## Developing providers

See [the docs](https://devsy.sh/docs/developing-providers/quickstart) to learn how to build a provider.

### Publishing your provider

When your provider is ready:

1. Add your provider to `community.yaml`.
2. Document it in `sites/docs-devsy-sh/content/docs/managing-providers/manage-providers.mdx`.

The docs and the UI then list your provider.

## Desktop deep links

Devsy Desktop handles deep links that run actions.

URL scheme:

```
devsy://command?param1=value1&param2=value2
```

### Open a workspace

Opens a workspace from a source, like `devsy workspace up` with a link:

```
devsy://open?source=<url-encoded-source>&workspace=<name>&provider=<provider>&ide=<ide>
```

Parameters:

- `source` (required): URL-encoded workspace source
- `workspace` (optional): Workspace name
- `provider` (optional): Provider to use
- `ide` (optional): IDE to open

Example:

```
devsy://open?source=https%3A%2F%2Fgithub.com%2Fuser%2Frepo&workspace=my-workspace&provider=docker&ide=vscode
```

### Import a workspace

Imports a Devsy Pro workspace into the local client:

```
devsy://import?workspace_id=<id>&workspace_uid=<uid>&devsy_pro_host=<host>&options=<options>
```

Parameters:

- `workspace_id` (required): Workspace ID
- `workspace_uid` (required): Workspace UID
- `devsy_pro_host` (required): Devsy Pro host URL
- `options` (optional): Additional options

## Repository structure

- `/cmd`: CLI command implementations
- `/pkg`: Core packages and libraries
- `/desktop`: Desktop application (Electron and Svelte)
- `/e2e`: End-to-end tests
- `/providers`: Built-in provider definitions
- `/sites`: Documentation and download websites
- `/hack`: Build and development scripts

## Pull requests

The pull request template asks for three things:

- AI disclosure: AI-assisted code is welcome. Name the tool, the model, and what it did. You must understand every line you submit.
- Integration tests: CLI changes need Ginkgo e2e tests in `e2e/tests/`. Desktop changes need Playwright e2e tests in `desktop/e2e/`. Unit tests alone do not cover them. If no integration test applies, say why in the PR.
- Conventional Commits title: the title becomes the squash commit subject.

## Getting help

- [Documentation](https://devsy.sh/docs)
- [GitHub Issues](https://github.com/devsy-org/devsy/issues)
- [GitHub Discussions](https://github.com/devsy-org/devsy/discussions)

## Code of conduct

Be [respectful and constructive](https://mikemcquaid.com/open-source-maintainers-owe-you-nothing/) with the community.
