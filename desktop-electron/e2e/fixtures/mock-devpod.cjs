#!/usr/bin/env node
"use strict"
// Mock devpod binary for e2e tests (cross-platform Node.js version)
// Returns canned JSON responses for each subcommand
// Handles --output json, --skip-pro, and other flags the app sends
// Persists state to a JSON file so provider/workspace CRUD works across calls

const fs = require("fs")
const path = require("path")
const os = require("os")

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
          source: { github: "devsy-org/devpod-provider-docker" },
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
          source: { github: "devsy-org/devpod-provider-kubernetes" },
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
        source: { gitRepository: "https://github.com/example/repo", gitBranch: "main" },
        provider: { name: "docker" },
        ide: { name: "vscode" },
        status: "Running",
        lastUsedTimestamp: "2026-04-22T10:00:00Z",
        creationTimestamp: "2026-04-20T08:00:00Z",
        context: "default",
      },
      {
        id: "dev-env",
        uid: "ws-002",
        source: { gitRepository: "https://github.com/example/dev" },
        provider: { name: "kubernetes" },
        ide: { name: "goland" },
        status: "Stopped",
        lastUsedTimestamp: "2026-04-21T15:00:00Z",
        creationTimestamp: "2026-04-19T09:00:00Z",
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

const args = process.argv.slice(2)

// Collect positional args and named flag values
const positional = []
let idFlag = ""
let providerFlag = ""
let ideFlag = ""
let nameFlag = ""
let i = 0
while (i < args.length) {
  const arg = args[i]
  if (arg === "--output") { i += 2; continue }
  if (arg === "--skip-pro" || arg === "--force" || arg.startsWith("--use=")) { i++; continue }
  if (arg === "-o" || arg === "--option") { i += 2; continue }
  if (arg === "--id") { idFlag = args[i + 1] || ""; i += 2; continue }
  if (arg === "--provider") { providerFlag = args[i + 1] || ""; i += 2; continue }
  if (arg === "--ide") { ideFlag = args[i + 1] || ""; i += 2; continue }
  if (arg === "--name") { nameFlag = args[i + 1] || ""; i += 2; continue }
  if (["--version", "--timeout"].includes(arg)) { i += 2; continue }
  if (arg === "--recreate" || arg === "--reset" || arg === "--dry-run") { i++; continue }
  if (arg.startsWith("-")) { i++; continue }
  positional.push(arg)
  i++
}

const cmd = positional[0] || ""
const sub = positional[1] || ""
const extra = positional[2] || ""

function out(data) {
  process.stdout.write(typeof data === "string" ? data + "\n" : JSON.stringify(data, null, 2) + "\n")
}

switch (cmd) {
  case "list":
    out(state.workspaces)
    break

  case "provider":
    switch (sub) {
      case "list":
        out(state.providers)
        break
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
            default: false,
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
        const newName = nameFlag
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
      case "use": {
        const provName = extra
        if (provName && state.providers[provName]) {
          for (const key of Object.keys(state.providers)) {
            state.providers[key].default = false
          }
          state.providers[provName].state.initialized = true
          state.providers[provName].default = true
          saveState(state)
        }
        out("")
        break
      }
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
      case "options":
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
      case "set-options":
      case "create":
      case "delete":
        out("")
        break
      default:
        out("")
        break
    }
    break

  case "status": {
    const ws = state.workspaces.find((w) => w.id === sub)
    if (ws) {
      out({ state: ws.status })
    } else {
      out({ state: "NotFound" })
    }
    break
  }

  case "version":
    out("v0.1.0-test")
    break

  case "ssh":
    // In tests, just exit cleanly
    process.exit(0)
    break

  case "up": {
    const source = sub
    out("Resolving source...")
    out("Pulling image...")
    out("Starting workspace...")
    out("Workspace ready.")
    const wsId = idFlag || (source ? source.split("/").pop().replace(".git", "") : "") || "workspace"
    state.workspaces.push({
      id: wsId,
      uid: "ws-" + Date.now(),
      source: { gitRepository: source },
      provider: { name: providerFlag || "docker" },
      ide: { name: ideFlag || "none" },
      status: "Running",
      lastUsedTimestamp: new Date().toISOString(),
      creationTimestamp: new Date().toISOString(),
      context: "default",
    })
    saveState(state)
    process.exit(0)
    break
  }

  case "stop": {
    const wsId = sub
    out("Stopping workspace...")
    out("Workspace stopped.")
    const ws = state.workspaces.find((w) => w.id === wsId)
    if (ws) {
      ws.status = "Stopped"
      saveState(state)
    }
    process.exit(0)
    break
  }

  case "delete": {
    const wsId = sub
    out("Deleting workspace...")
    out("Workspace deleted.")
    const idx = state.workspaces.findIndex((w) => w.id === wsId)
    if (idx !== -1) {
      state.workspaces.splice(idx, 1)
      saveState(state)
    }
    process.exit(0)
    break
  }

  case "upgrade":
    out("Already up to date.")
    process.exit(0)
    break

  default:
    process.stderr.write(`mock-devpod: unknown command '${cmd}'\n`)
    process.exit(1)
}
