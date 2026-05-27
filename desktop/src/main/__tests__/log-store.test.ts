import { mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, beforeEach, describe, expect, it } from "vitest"
import { LogStore } from "../log-store.js"

const CTX = "default"

describe("LogStore", () => {
  let store: LogStore
  let tempDir: string

  beforeEach(() => {
    tempDir = mkdtempSync(join(tmpdir(), "logstore-test-"))
    store = new LogStore(tempDir)
  })

  afterEach(() => {
    rmSync(tempDir, { recursive: true, force: true })
  })

  it("creates a log file under the per-workspace dir", () => {
    const logPath = store.createLogFile(CTX, "ws-1")
    expect(logPath).toContain(
      join("contexts", CTX, "workspaces", "ws-1", "logs"),
    )
    expect(logPath).toMatch(/\.log$/)
  })

  it("appends lines to a log file", () => {
    const logPath = store.createLogFile(CTX, "ws-1")
    store.appendLog(logPath, "line 1")
    store.appendLog(logPath, "line 2")
    const content = store.readLogByPath(logPath)
    expect(content).toContain("line 1")
    expect(content).toContain("line 2")
  })

  it("lists logs for a workspace, newest first", () => {
    store.createLogFile(CTX, "ws-1")
    const path2 = store.createLogFile(CTX, "ws-1")
    const entries = store.listLogs(CTX, "ws-1")
    expect(entries).toHaveLength(2)
    expect(entries[0].filename).toBe(
      path2.split("/").pop()?.split("\\").pop() ?? "",
    )
  })

  it("returns empty list for unknown workspace", () => {
    expect(store.listLogs(CTX, "nonexistent")).toEqual([])
  })

  it("reads a log file by workspace and filename", () => {
    const logPath = store.createLogFile(CTX, "ws-1")
    store.appendLog(logPath, "test content")
    const entries = store.listLogs(CTX, "ws-1")
    const content = store.readLog(CTX, "ws-1", entries[0].filename)
    expect(content).toContain("test content")
  })

  it("deletes a log file", () => {
    const logPath = store.createLogFile(CTX, "ws-1")
    store.appendLog(logPath, "data")
    const entries = store.listLogs(CTX, "ws-1")
    store.deleteLog(CTX, "ws-1", entries[0].filename)
    expect(store.listLogs(CTX, "ws-1")).toHaveLength(0)
  })

  it("prunes logs older than maxAge (keeps recent)", () => {
    store.createLogFile(CTX, "ws-1")
    const removed = store.prune(30)
    expect(removed).toBe(0)
    expect(store.listLogs(CTX, "ws-1")).toHaveLength(1)
  })

  it("isolates logs per workspace and per context", () => {
    store.createLogFile(CTX, "ws-1")
    store.createLogFile("other-ctx", "ws-1")
    store.createLogFile(CTX, "ws-2")
    expect(store.listLogs(CTX, "ws-1")).toHaveLength(1)
    expect(store.listLogs("other-ctx", "ws-1")).toHaveLength(1)
    expect(store.listLogs(CTX, "ws-2")).toHaveLength(1)
  })

  it("rejects non-.log basenames after stripping traversal", () => {
    // basename("../../etc/passwd") = "passwd" — no .log extension, rejected.
    expect(() => store.readLog(CTX, "ws-1", "../../etc/passwd")).toThrow(
      /invalid log filename/,
    )
    expect(() => store.deleteLog(CTX, "ws-1", "../../etc/passwd")).toThrow(
      /invalid log filename/,
    )
    expect(() => store.readLog(CTX, "ws-1", "notes.txt")).toThrow(
      /invalid log filename/,
    )
  })

  it("confines traversal-shaped .log filenames to the workspace dir", () => {
    // Plant a sibling.log OUTSIDE the workspace logs dir.
    const outside = join(tempDir, "sibling.log")
    writeFileSync(outside, "outside-content")
    // basename("../sibling.log") = "sibling.log" → looked up INSIDE the
    // workspace logs dir, where nothing of that name exists. We must NOT
    // read the planted file.
    expect(() => store.readLog(CTX, "ws-1", "../sibling.log")).toThrow(
      /ENOENT/,
    )
  })

  it("prune skips non-directory entries without aborting", () => {
    // Create a normal log first.
    store.createLogFile(CTX, "ws-1")
    // Plant a regular file where prune would expect a workspace dir.
    const contextsRoot = join(tempDir, "contexts", CTX, "workspaces")
    const stray = join(contextsRoot, "stray-file")
    writeFileSync(stray, "")
    expect(() => store.prune(30)).not.toThrow()
    expect(store.listLogs(CTX, "ws-1")).toHaveLength(1)
  })
})
