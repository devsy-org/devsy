#!/usr/bin/env node
"use strict"
// Mock devsy binary for e2e tests (cross-platform Node.js version)
// Returns canned JSON responses for each subcommand
// Handles --result-format json, --skip-pro, and other flags the app sends
// Persists state to a JSON file so provider/workspace CRUD works across calls

const fs = require("node:fs")
const path = require("node:path")
const os = require("node:os")

const STATE_FILE = path.join(os.tmpdir(), "devsy-mock-state.json")

function defaultState() {
  return {
    providers: {
      docker: {
        config: {
          name: "docker",
          version: "v0.5.0",
          icon: "https://devsy.sh/icons/docker.svg",
          description: "Devsy on Docker",
          source: { github: "devsy-org/devsy-provider-docker" },
          options: {
            DOCKER_HOST: {
              displayName: "Docker Host",
              description: "Docker daemon socket to connect to",
              default: "unix:///var/run/docker.sock",
              type: "string",
            },
          },
          optionGroups: [],
        },
        state: { initialized: true },
        default: true,
      },
      kubernetes: {
        config: {
          name: "kubernetes",
          version: "v0.3.0",
          icon: "https://devsy.sh/icons/k8s.svg",
          description: "Devsy on Kubernetes",
          source: { github: "devsy-org/devsy-provider-kubernetes" },
          options: {},
          optionGroups: [],
        },
        state: { initialized: false },
        default: false,
      },
    },
    workspaces: [
      {
        id: "test-workspace",
        uid: "ws-001",
        source: {
          gitRepository: "https://github.com/example/repo",
          gitBranch: "main",
        },
        provider: { name: "docker" },
        ide: { name: "vscode" },
        status: "Running",
        lastUsed: "2026-04-22T10:00:00Z",
        created: "2026-04-20T08:00:00Z",
        context: "default",
      },
      {
        id: "dev-env",
        uid: "ws-002",
        source: { gitRepository: "https://github.com/example/dev" },
        provider: { name: "kubernetes" },
        ide: { name: "goland" },
        status: "Stopped",
        lastUsed: "2026-04-21T15:00:00Z",
        created: "2026-04-19T09:00:00Z",
        context: "default",
      },
    ],
  }
}

function loadState() {
  try {
    const data = fs.readFileSync(STATE_FILE, "utf8")
    return JSON.parse(data)
  } catch {
    return defaultState()
  }
}

function saveState(state) {
  fs.writeFileSync(STATE_FILE, JSON.stringify(state, null, 2), "utf8")
}

const state = loadState()

const rawArgs = process.argv.slice(2)

if (rawArgs[0] === "--version") {
  process.stdout.write("v0.1.0-test\n")
  process.exit(0)
}

function out(data) {
  process.stdout.write(
    typeof data === "string"
      ? `${data}\n`
      : `${JSON.stringify(data, null, 2)}\n`,
  )
}

// Parse a slice of args (positional + recognized flags) into a result object.
function parseArgs(args) {
  const positional = []
  let idFlag = ""
  let providerFlag = ""
  let ideFlag = ""
  let nameFlag = ""
  let i = 0
  while (i < args.length) {
    const arg = args[i]
    if (arg === "--result-format") {
      i += 2
      continue
    }
    if (arg === "--skip-pro" || arg === "--force" || arg.startsWith("--use=")) {
      i++
      continue
    }
    if (arg === "-o" || arg === "--option") {
      i += 2
      continue
    }
    if (arg === "--id") {
      idFlag = args[i + 1] || ""
      i += 2
      continue
    }
    if (arg === "--provider") {
      providerFlag = args[i + 1] || ""
      i += 2
      continue
    }
    if (arg === "--ide") {
      ideFlag = args[i + 1] || ""
      i += 2
      continue
    }
    if (arg === "--name") {
      nameFlag = args[i + 1] || ""
      i += 2
      continue
    }
    if (["--version", "--timeout"].includes(arg)) {
      i += 2
      continue
    }
    if (arg === "--recreate" || arg === "--reset" || arg === "--dry-run") {
      i++
      continue
    }
    if (arg.startsWith("-")) {
      i++
      continue
    }
    positional.push(arg)
    i++
  }
  return { positional, idFlag, providerFlag, ideFlag, nameFlag }
}

// Workspace verb handlers. Each accepts the slice of args AFTER its verb,
// so handlers behave identically whether invoked as `<verb> ...` (root
// shortcut) or `workspace <verb> ...` (canonical form).
function handleList() {
  out(state.workspaces)
}

function handleStatus(args) {
  const { positional } = parseArgs(args)
  const target = positional[0]
  const ws = state.workspaces.find((w) => w.id === target)
  if (ws) {
    out({ state: ws.status })
  } else {
    out({ state: "NotFound" })
  }
}

function handleSsh() {
  process.exit(0)
}

function handleUp(args) {
  const { positional, idFlag, providerFlag, ideFlag } = parseArgs(args)
  const source = positional[0]
  out("Resolving source...")
  out("Pulling image...")
  out("Starting workspace...")
  out("Workspace ready.")
  const wsId =
    idFlag ||
    (source ? source.split("/").pop().replace(".git", "") : "") ||
    "workspace"
  state.workspaces.push({
    id: wsId,
    uid: `ws-${Date.now()}`,
    source: { gitRepository: source },
    provider: { name: providerFlag || "docker" },
    ide: { name: ideFlag || "none" },
    status: "Running",
    lastUsed: new Date().toISOString(),
    created: new Date().toISOString(),
    context: "default",
  })
  saveState(state)
  process.exit(0)
}

function handleStop(args) {
  const { positional } = parseArgs(args)
  const wsId = positional[0]
  out("Stopping workspace...")
  out("Workspace stopped.")
  const ws = state.workspaces.find((w) => w.id === wsId)
  if (ws) {
    ws.status = "Stopped"
    saveState(state)
  }
  process.exit(0)
}

function handleDelete(args) {
  const { positional } = parseArgs(args)
  const wsId = positional[0]
  out("Deleting workspace...")
  out("Workspace deleted.")
  const idx = state.workspaces.findIndex((w) => w.id === wsId)
  if (idx !== -1) {
    state.workspaces.splice(idx, 1)
    saveState(state)
  }
  process.exit(0)
}

function handleRename(args) {
  const { positional } = parseArgs(args)
  const oldId = positional[0]
  const newId = positional[1]
  if (oldId && newId) {
    const idx = state.workspaces.findIndex((w) => w.id === oldId)
    if (idx !== -1) {
      state.workspaces[idx].id = newId
      saveState(state)
    }
  }
  out("")
}

// Unimplemented workspace verbs: exit 0 with empty output.
function handleNoop() {
  out("")
}

const workspaceHandlers = {
  list: handleList,
  ls: handleList,
  status: handleStatus,
  ssh: handleSsh,
  up: handleUp,
  stop: handleStop,
  delete: handleDelete,
  rename: handleRename,
  logs: handleNoop,
  exec: handleNoop,
  build: handleNoop,
  export: handleNoop,
  import: handleNoop,
  ping: handleNoop,
  troubleshoot: handleNoop,
}

// Feature verb handlers.
function handleFeatureUpgrade() {
  out("Already up to date.")
  process.exit(0)
}

function handleFeatureNoop() {
  out("")
  process.exit(0)
}

const featureHandlers = {
  upgrade: handleFeatureUpgrade,
  outdated: handleFeatureNoop,
  info: handleFeatureNoop,
  manifest: handleFeatureNoop,
  tags: handleFeatureNoop,
  package: handleFeatureNoop,
  publish: handleFeatureNoop,
  test: handleFeatureNoop,
  "resolve-deps": handleFeatureNoop,
  docs: handleFeatureNoop,
}

const cmd = rawArgs[0] || ""

// Canonical form: `devsy feature <verb> ...`
if (cmd === "feature") {
  const verb = rawArgs[1]
  const handler = featureHandlers[verb]
  if (!handler) {
    process.stderr.write(`mock-devsy: unknown feature subcommand '${verb}'\n`)
    process.exit(2)
  }
  handler(rawArgs.slice(2))
  process.exit(0)
}

// Canonical form: `devsy workspace <verb> ...`
if (cmd === "workspace") {
  const verb = rawArgs[1]
  const handler = workspaceHandlers[verb]
  if (!handler) {
    process.stderr.write(`mock-devsy: unknown workspace subcommand '${verb}'\n`)
    process.exit(2)
  }
  handler(rawArgs.slice(2))
  process.exit(0)
}

// Non-workspace top-level commands (preserved verbatim).
const parsed = parseArgs(rawArgs)
const sub = parsed.positional[1] || ""
const extra = parsed.positional[2] || ""
const extra2 = parsed.positional[3] || ""
const { nameFlag } = parsed

switch (cmd) {
  case "provider":
    switch (sub) {
      case "list":
        out(state.providers)
        break
      case "get":
      case "options":
        out({
          DOCKER_HOST: {
            name: "DOCKER_HOST",
            displayName: "Docker Host",
            description: "Docker daemon socket to connect to",
            default: "unix:///var/run/docker.sock",
            value: "unix:///var/run/docker.sock",
            type: "string",
          },
          DISK_SIZE: {
            name: "DISK_SIZE",
            displayName: "Disk Size",
            description: "Size of the disk in GB",
            default: "30",
            value: "30",
            type: "string",
          },
        })
        break
      case "add": {
        const provName = extra
        if (provName) {
          for (const key of Object.keys(state.providers)) {
            state.providers[key].default = false
          }
          state.providers[provName] = {
            config: {
              name: provName,
              version: "v0.1.0",
              icon: "",
              description: "",
              source: {},
              options: {},
              optionGroups: [],
            },
            state: { initialized: false },
            default: true,
          }
          saveState(state)
        }
        out("")
        break
      }
      case "delete": {
        const provName = extra
        if (provName && state.providers[provName]) {
          delete state.providers[provName]
          saveState(state)
        }
        out("")
        break
      }
      case "rename": {
        const oldName = extra
        const newName = nameFlag || extra2
        if (oldName && newName && state.providers[oldName]) {
          const entry = state.providers[oldName]
          entry.config.name = newName
          state.providers[newName] = entry
          delete state.providers[oldName]
          saveState(state)
        }
        out("")
        break
      }
      case "configure": {
        const provName = extra
        if (provName && state.providers[provName]) {
          state.providers[provName].state.initialized = true
          saveState(state)
        }
        out("")
        break
      }
      case "use": {
        const provName = extra
        if (provName && state.providers[provName]) {
          for (const key of Object.keys(state.providers)) {
            state.providers[key].default = false
          }
          state.providers[provName].default = true
          saveState(state)
        }
        out("")
        break
      }
      case "versions":
        out([])
        break
      case "set":
      case "set-options":
      case "update":
        out("")
        break
      default:
        out("")
        break
    }
    break

  case "machine":
    switch (sub) {
      case "list":
        out([])
        break
      case "status":
        out({ state: "Running" })
        break
      case "create":
      case "delete":
      case "start":
      case "stop":
        out("")
        break
      default:
        out("")
        break
    }
    break

  case "context":
    switch (sub) {
      case "list":
        out([
          { name: "default", default: true },
          { name: "staging", default: false },
        ])
        break
      case "get":
        out({
          TELEMETRY: { value: "true" },
          AGENT_URL: { value: "" },
          DOTFILES_URL: { value: "" },
          DOTFILES_SCRIPT: { value: "" },
          SSH_INJECT_DOCKER_CREDENTIALS: { value: "true" },
          SSH_INJECT_GIT_CREDENTIALS: { value: "true" },
          GIT_SSH_SIGNATURE_FORWARDING: { value: "true" },
          SSH_AGENT_FORWARDING: { value: "true" },
          SSH_ADD_PRIVATE_KEYS: { value: "true" },
          SSH_STRICT_HOST_KEY_CHECKING: { value: "false" },
          GPG_AGENT_FORWARDING: { value: "false" },
          AGENT_INJECT_TIMEOUT: { value: "20" },
          REGISTRY_CACHE: { value: "" },
          EXIT_AFTER_TIMEOUT: { value: "true" },
          SSH_CONFIG_PATH: { value: "" },
          SSH_CONFIG_INCLUDE_PATH: { value: "" },
        })
        break
      case "use":
      case "set":
      case "create":
      case "delete":
        out("")
        break
      default:
        out("")
        break
    }
    break

  case "version":
    out("v0.1.0-test")
    break

  default:
    process.stderr.write(`mock-devsy: unknown command '${cmd}'\n`)
    process.exit(1)
}
