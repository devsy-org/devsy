// @vitest-environment node
import { homedir, platform } from "node:os"
import { join } from "node:path"
import type { IPty } from "node-pty"
import { describe, expect, it, vi } from "vitest"
import { PtyManager, sshTerminalArgs } from "../pty.js"

describe("PtyManager", () => {
  it("keeps the bundled CLI and standard user-local tools ahead of inherited shims", () => {
    const inheritedPath = "/fixture/mise/shims:/usr/bin:/bin"
    vi.stubEnv("PATH", inheritedPath)
    try {
      const proc = { onData: vi.fn(), onExit: vi.fn() } as unknown as IPty
      const spawnPty = vi.fn<typeof import("node-pty").spawn>(() => proc)
      const manager = new PtyManager({
        binaryPath: "/bundle/devsy",
        getMainWindow: () => null,
        spawnPty,
      })
      manager.createSshSession("workspace-id", 80, 24)
      const options = spawnPty.mock.calls[0]?.[2] as {
        env: Record<string, string>
      }
      const path = options.env.PATH
      expect(path.startsWith("/bundle:")).toBe(true)
      if (platform() === "darwin") {
        const directories = path.split(":")
        const userBin = join(homedir(), ".local", "bin")
        expect(directories).toContain(userBin)
        expect(directories.indexOf(userBin)).toBeLessThan(
          directories.indexOf("/fixture/mise/shims"),
        )
      } else {
        expect(path).toBe(`/bundle:${inheritedPath}`)
      }
      expect(process.env.PATH).toBe(inheritedPath)
    } finally {
      vi.unstubAllEnvs()
    }
  })

  it("starts local shells in the user's home directory", () => {
    const proc = {
      onData: vi.fn(),
      onExit: vi.fn(),
    } as unknown as IPty
    const spawnPty = vi.fn(() => proc)
    const manager = new PtyManager({
      binaryPath: "/devsy",
      getMainWindow: () => null,
      spawnPty,
    })

    manager.createSession(80, 24)

    expect(spawnPty).toHaveBeenCalledWith(
      platform() === "win32"
        ? "powershell.exe"
        : process.env.SHELL || "/bin/sh",
      [],
      expect.objectContaining({ cwd: homedir(), cols: 80, rows: 24 }),
    )
  })

  it("starts SSH terminals with error-only Devsy logs", () => {
    expect(sshTerminalArgs("workspace-id")).toEqual([
      "--log-level=error",
      "workspace",
      "ssh",
      "workspace-id",
    ])
  })

  it("passes a session-scoped diagnostic log path to the SSH CLI", () => {
    const proc = {
      onData: vi.fn(),
      onExit: vi.fn(),
    } as unknown as IPty
    const spawnPty = vi
      .fn()
      .mockReturnValue(proc) as unknown as typeof import("node-pty").spawn
    const manager = new PtyManager({
      binaryPath: "/devsy",
      getMainWindow: () => null,
      spawnPty,
    })

    const sessionId = manager.createSshSession(
      "workspace-id",
      80,
      24,
      "/logs/ssh-diagnostics.log",
    )

    const options = spawnPty.mock.calls[0]?.[2] as {
      env: Record<string, string>
    }
    expect(options.env.DEVSY_GPG_FORWARD_DIAGNOSTIC_FILE).toBe(
      "/logs/ssh-diagnostics.log",
    )
    expect(options.env.DEVSY_GPG_FORWARD_SESSION_ID).toBe(sessionId)
  })
})
