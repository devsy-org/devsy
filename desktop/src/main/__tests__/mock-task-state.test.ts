import { createRequire } from "node:module"
import { once } from "node:events"
import { spawn, type ChildProcess } from "node:child_process"
import { mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { describe, expect, it } from "vitest"

const require = createRequire(import.meta.url)
const { mergeTasks } = require("../../../e2e/fixtures/mock-task-state.cjs") as {
  mergeTasks: (
    current: Record<string, { status: string }>,
    update: Record<string, { status: string }>,
    deletedIds?: string[],
    tombstones?: Record<string, boolean>,
  ) => Record<string, { status: string }>
}

describe("mock detached task persistence", () => {
  it("preserves tasks created after a process loaded its state snapshot", () => {
    const current = { "task-new": { status: "pending" } }
    const staleUpdate = { "task-old": { status: "succeeded" } }

    expect(mergeTasks(current, staleUpdate)).toEqual({
      "task-new": { status: "pending" },
      "task-old": { status: "succeeded" },
    })
  })

  it("does not let a stale pending snapshot undo task cancellation", () => {
    expect(
      mergeTasks(
        { "task-1": { status: "failed" } },
        { "task-1": { status: "pending" } },
      ),
    ).toEqual({ "task-1": { status: "failed" } })
  })

  it("does not let stale completion overwrite task cancellation", () => {
    expect(
      mergeTasks(
        { "task-1": { status: "failed" } },
        { "task-1": { status: "succeeded" } },
      ),
    ).toEqual({ "task-1": { status: "failed" } })
  })

  it("applies explicit task removal", () => {
    expect(
      mergeTasks(
        { "task-1": { status: "succeeded" } },
        {},
        ["task-1"],
      ),
    ).toEqual({})
  })

  it("does not recreate a task covered by a deletion tombstone", () => {
    expect(
      mergeTasks(
        {},
        { "task-1": { status: "pending" } },
        [],
        { "task-1": true },
      ),
    ).toEqual({})
  })

  it("does not restore a failed task when its ID is explicitly deleted", () => {
    expect(
      mergeTasks(
        { "task-1": { status: "failed" } },
        { "task-1": { status: "pending" } },
        ["task-1"],
      ),
    ).toEqual({})
  })

  it("serializes updates from separate processes", async () => {
    const directory = mkdtempSync(join(tmpdir(), "mock-task-lock-"))
    const lockPath = join(directory, "state.lock")
    const releasePath = join(directory, "release")
    const helperPath = require.resolve(
      "../../../e2e/fixtures/mock-task-state.cjs",
    )
    const children: ChildProcess[] = []
    const exits: Promise<unknown>[] = []
    let output = ""
    const observe = (child: ChildProcess) => {
      child.stdout?.on("data", (chunk: Buffer) => {
        output += chunk.toString()
      })
      return (line: string) =>
        new Promise<void>((resolve, reject) => {
          const check = () => {
            if (output.includes(line)) resolve()
          }
          child.stdout?.on("data", check)
          child.once("exit", (code) => {
            if (!output.includes(line))
              reject(new Error(`child exited with ${code} before ${line}`))
          })
          check()
        })
    }
    const launch = (source: string) => {
      const child = spawn(process.execPath, ["-e", source], {
        env: {
          ...process.env,
          HELPER_PATH: helperPath,
          LOCK_PATH: lockPath,
          RELEASE_PATH: releasePath,
        },
        stdio: ["ignore", "pipe", "inherit"],
      })
      children.push(child)
      exits.push(once(child, "exit"))
      return { child, waitFor: observe(child) }
    }

    try {
      const first = launch(`
        const fs = require("node:fs")
        const { withFileLock } = require(process.env.HELPER_PATH)
        withFileLock(process.env.LOCK_PATH, () => {
          process.stdout.write("locked\\n")
          const cell = new Int32Array(new SharedArrayBuffer(4))
          while (!fs.existsSync(process.env.RELEASE_PATH)) Atomics.wait(cell, 0, 0, 10)
        })
      `)
      await first.waitFor("locked")

      const second = launch(`
        const { withFileLock } = require(process.env.HELPER_PATH)
        process.stdout.write("waiting\\n")
        withFileLock(process.env.LOCK_PATH, () => process.stdout.write("entered\\n"))
      `)
      await second.waitFor("waiting")
      expect(output).not.toContain("entered")

      writeFileSync(releasePath, "")
      await second.waitFor("entered")
      await Promise.all(exits)
    } finally {
      writeFileSync(releasePath, "")
      for (const child of children) {
        if (child.exitCode === null) child.kill()
      }
      await Promise.all(exits)
      rmSync(directory, { recursive: true, force: true })
    }
  })
})
