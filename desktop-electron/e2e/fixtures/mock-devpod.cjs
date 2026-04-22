#!/usr/bin/env node
"use strict"
// Mock devpod binary for e2e tests (cross-platform Node.js version)
// Returns canned JSON responses for each subcommand
// Handles --output json, --skip-pro, and other flags the app sends

const args = process.argv.slice(2)

// Collect positional args, skipping flags
const positional = []
let i = 0
while (i < args.length) {
  const arg = args[i]
  if (arg === "--output") { i += 2; continue }
  if (arg === "--skip-pro" || arg === "--force" || arg.startsWith("--use=")) { i++; continue }
  if (arg === "-o" || arg === "--option") { i += 2; continue }
  if (["--id", "--provider", "--ide", "--name", "--version", "--timeout"].includes(arg)) { i += 2; continue }
  if (arg === "--recreate" || arg === "--reset" || arg === "--dry-run") { i++; continue }
  if (arg.startsWith("-")) { i++; continue }
  positional.push(arg)
  i++
}

const cmd = positional[0] || ""
const sub = positional[1] || ""

function out(data) {
  process.stdout.write(typeof data === "string" ? data + "\n" : JSON.stringify(data, null, 2) + "\n")
}

switch (cmd) {
  case "list":
    out([
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
    ])
    break

  case "provider":
    switch (sub) {
      case "list":
        out({
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
        })
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
      case "add":
      case "delete":
      case "use":
      case "update":
      case "set-options":
      case "rename":
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

  case "status":
    // Return per-workspace status based on workspace ID
    switch (sub) {
      case "dev-env":
        out({ state: "Stopped" })
        break
      default:
        out({ state: "Running" })
        break
    }
    break

  case "version":
    out("v0.1.0-test")
    break

  case "ssh":
    // In tests, just exit cleanly
    process.exit(0)
    break

  case "up":
    out("Resolving source...")
    out("Pulling image...")
    out("Starting workspace...")
    out("Workspace ready.")
    process.exit(0)
    break

  case "stop":
    out("Stopping workspace...")
    out("Workspace stopped.")
    process.exit(0)
    break

  case "delete":
    out("Deleting workspace...")
    out("Workspace deleted.")
    process.exit(0)
    break

  case "upgrade":
    out("Already up to date.")
    process.exit(0)
    break

  default:
    process.stderr.write(`mock-devpod: unknown command '${cmd}'\n`)
    process.exit(1)
}
