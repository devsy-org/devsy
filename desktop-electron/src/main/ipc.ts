import { ipcMain } from "electron"
import type { BrowserWindow } from "electron"
import type { CliRunner } from "./cli.js"
import type { DaemonState } from "./state.js"
import type { LogStore } from "./log-store.js"

interface IpcDependencies {
  cli: CliRunner
  state: DaemonState
  logStore: LogStore
  getMainWindow: () => BrowserWindow | null
}

export function registerIpcHandlers(deps: IpcDependencies): void {
  const { cli, state, logStore } = deps

  // ── Workspaces ──
  ipcMain.handle("workspace_list", () => state.workspaceList())

  ipcMain.handle("workspace_status", async (_event, args: { workspaceId: string }) => {
    return cli.runRaw(["status", args.workspaceId, "--output", "json", "--timeout", "5s"])
  })

  // ── Providers ──
  ipcMain.handle("provider_list", () => state.providerList())

  ipcMain.handle("provider_add", async (_event, args: { name: string; source?: string }) => {
    const src = args.source ?? args.name
    const cliArgs = ["provider", "add", src, "--use=false"]
    if (args.source) {
      cliArgs.push("--name", args.name)
    }
    await cli.runRaw(cliArgs)
  })

  ipcMain.handle("provider_delete", async (_event, args: { name: string }) => {
    await cli.runRaw(["provider", "delete", args.name])
  })

  ipcMain.handle("provider_use", async (_event, args: { name: string }) => {
    await cli.runRaw(["provider", "use", args.name])
  })

  ipcMain.handle("provider_update", async (_event, args: { name: string }) => {
    await cli.runRaw(["provider", "update", args.name])
  })

  ipcMain.handle("provider_options", async (_event, args: { name: string }) => {
    return cli.run(["provider", "options", args.name])
  })

  ipcMain.handle(
    "provider_set_options",
    async (_event, args: { name: string; options: string[] }) => {
      const cliArgs = ["provider", "set-options", args.name]
      for (const opt of args.options) {
        cliArgs.push("-o", opt)
      }
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle(
    "provider_rename",
    async (_event, args: { name: string; newName: string }) => {
      await cli.runRaw(["provider", "rename", args.name, args.newName])
    },
  )

  // ── Machines ──
  ipcMain.handle("machine_list", () => state.machineList())

  ipcMain.handle(
    "machine_create",
    async (_event, args: { name: string; provider: string; options?: Record<string, string> }) => {
      const cliArgs = ["machine", "create", args.name, "--provider", args.provider]
      for (const [k, v] of Object.entries(args.options ?? {})) {
        cliArgs.push("--option", `${k}=${v}`)
      }
      await cli.runRaw(cliArgs)
    },
  )

  ipcMain.handle("machine_delete", async (_event, args: { id: string; force?: boolean }) => {
    const cliArgs = ["machine", "delete", args.id]
    if (args.force) cliArgs.push("--force")
    await cli.runRaw(cliArgs)
  })

  ipcMain.handle("machine_start", async (_event, args: { id: string }) => {
    await cli.runRaw(["machine", "start", args.id])
  })

  ipcMain.handle("machine_stop", async (_event, args: { id: string }) => {
    await cli.runRaw(["machine", "stop", args.id])
  })

  ipcMain.handle("machine_status", async (_event, args: { id: string }) => {
    return cli.runRaw(["machine", "status", args.id, "--output", "json"])
  })

  // ── Contexts ──
  ipcMain.handle("context_list", () => state.contextList())

  ipcMain.handle("context_use", async (_event, args: { name: string }) => {
    await cli.runRaw(["context", "use", args.name])
  })

  ipcMain.handle("context_options", async (_event, args: { context?: string }) => {
    const cliArgs = ["context", "options"]
    if (args.context) cliArgs.push("--name", args.context)
    return cli.run(cliArgs)
  })

  ipcMain.handle("context_set_options", async (_event, args: { options: string[]; context?: string }) => {
    const cliArgs: string[] = ["context", "set-options"]
    if (args.context) cliArgs.push("--name", args.context)
    for (const opt of args.options) {
      cliArgs.push("-o", opt)
    }
    await cli.runRaw(cliArgs)
  })

  // ── System ──
  ipcMain.handle("devpod_version", async () => {
    return cli.runRaw(["version"])
  })

  ipcMain.handle("devpod_upgrade", async (_event, args: { version: string }) => {
    return cli.runRaw(["upgrade", "--version", args.version])
  })

  ipcMain.handle("devpod_upgrade_dry_run", async (_event, args: { version: string }) => {
    return cli.runRaw(["upgrade", "--version", args.version, "--dry-run"])
  })

  // ── Logs ──
  ipcMain.handle("workspace_logs_list", async (_event, args: { workspaceId: string }) => {
    return logStore.listLogs(args.workspaceId)
  })

  ipcMain.handle(
    "workspace_log_read",
    async (_event, args: { workspaceId: string; filename: string }) => {
      return logStore.readLog(args.workspaceId, args.filename)
    },
  )

  ipcMain.handle(
    "workspace_log_delete",
    async (_event, args: { workspaceId: string; filename: string }) => {
      logStore.deleteLog(args.workspaceId, args.filename)
    },
  )

  ipcMain.handle(
    "workspace_up",
    async (
      _event,
      args: { source: string; workspaceId?: string; provider?: string; ide?: string },
    ) => {
      const cliArgs = ["up", args.source]
      if (args.workspaceId) cliArgs.push("--id", args.workspaceId)
      if (args.provider) cliArgs.push("--provider", args.provider)
      if (args.ide) cliArgs.push("--ide", args.ide)

      const wsId = args.workspaceId ?? args.source
      const cmdId = crypto.randomUUID()
      const logPath = logStore.createLogFile(wsId)
      const win = deps.getMainWindow()

      cli.runStreaming(
        cliArgs,
        (line) => {
          logStore.appendLog(logPath, line)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: line,
            done: false,
          })
        },
        (code) => {
          const exitMsg = `Exit code: ${code}`
          logStore.appendLog(logPath, exitMsg)
          win?.webContents.send("command-progress", {
            commandId: cmdId,
            message: exitMsg,
            done: true,
          })
        },
      )

      return cmdId
    },
  )

  ipcMain.handle("workspace_stop", async (_event, args: { workspaceId: string }) => {
    const cmdId = crypto.randomUUID()
    const logPath = logStore.createLogFile(args.workspaceId)
    const win = deps.getMainWindow()

    cli.runStreaming(
      ["stop", args.workspaceId],
      (line) => {
        logStore.appendLog(logPath, line)
        win?.webContents.send("command-progress", { commandId: cmdId, message: line, done: false })
      },
      (code) => {
        const exitMsg = `Exit code: ${code}`
        logStore.appendLog(logPath, exitMsg)
        win?.webContents.send("command-progress", { commandId: cmdId, message: exitMsg, done: true })
      },
    )

    return cmdId
  })

  ipcMain.handle("workspace_delete", async (_event, args: { workspaceId: string }) => {
    const cmdId = crypto.randomUUID()
    const logPath = logStore.createLogFile(args.workspaceId)
    const win = deps.getMainWindow()

    cli.runStreaming(
      ["delete", args.workspaceId, "--force"],
      (line) => {
        logStore.appendLog(logPath, line)
        win?.webContents.send("command-progress", { commandId: cmdId, message: line, done: false })
      },
      (code) => {
        const exitMsg = `Exit code: ${code}`
        logStore.appendLog(logPath, exitMsg)
        win?.webContents.send("command-progress", { commandId: cmdId, message: exitMsg, done: true })
      },
    )

    return cmdId
  })

  ipcMain.handle("workspace_rebuild", async (_event, args: { workspaceId: string }) => {
    const cmdId = crypto.randomUUID()
    const logPath = logStore.createLogFile(args.workspaceId)
    const win = deps.getMainWindow()

    cli.runStreaming(
      ["up", args.workspaceId, "--recreate"],
      (line) => {
        logStore.appendLog(logPath, line)
        win?.webContents.send("command-progress", { commandId: cmdId, message: line, done: false })
      },
      (code) => {
        const exitMsg = `Exit code: ${code}`
        logStore.appendLog(logPath, exitMsg)
        win?.webContents.send("command-progress", { commandId: cmdId, message: exitMsg, done: true })
      },
    )

    return cmdId
  })

  ipcMain.handle("workspace_reset", async (_event, args: { workspaceId: string }) => {
    const cmdId = crypto.randomUUID()
    const logPath = logStore.createLogFile(args.workspaceId)
    const win = deps.getMainWindow()

    cli.runStreaming(
      ["up", args.workspaceId, "--reset"],
      (line) => {
        logStore.appendLog(logPath, line)
        win?.webContents.send("command-progress", { commandId: cmdId, message: line, done: false })
      },
      (code) => {
        const exitMsg = `Exit code: ${code}`
        logStore.appendLog(logPath, exitMsg)
        win?.webContents.send("command-progress", { commandId: cmdId, message: exitMsg, done: true })
      },
    )

    return cmdId
  })
}
