import { describe, it, expect, beforeEach, afterEach } from "vitest"
import { LogStore } from "../log-store.js"
import { mkdtempSync, rmSync } from "node:fs"
import { join } from "node:path"
import { tmpdir } from "node:os"

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

  it("creates a log file and returns its path", () => {
    const logPath = store.createLogFile("ws-1")
    expect(logPath).toContain("ws-1")
    expect(logPath).toMatch(/\.log$/)
  })

  it("appends lines to a log file", () => {
    const logPath = store.createLogFile("ws-1")
    store.appendLog(logPath, "line 1")
    store.appendLog(logPath, "line 2")
    const content = store.readLogByPath(logPath)
    expect(content).toContain("line 1")
    expect(content).toContain("line 2")
  })

  it("lists logs for a workspace, newest first", () => {
    store.createLogFile("ws-1")
    // Small delay to ensure different timestamps
    const path2 = store.createLogFile("ws-1")
    const entries = store.listLogs("ws-1")
    expect(entries).toHaveLength(2)
    expect(entries[0].filename).toBe(path2.split("/").pop()!.split("\\").pop()!)
  })

  it("returns empty list for unknown workspace", () => {
    expect(store.listLogs("nonexistent")).toEqual([])
  })

  it("reads a log file by workspace and filename", () => {
    const logPath = store.createLogFile("ws-1")
    store.appendLog(logPath, "test content")
    const entries = store.listLogs("ws-1")
    const content = store.readLog("ws-1", entries[0].filename)
    expect(content).toContain("test content")
  })

  it("deletes a log file", () => {
    const logPath = store.createLogFile("ws-1")
    store.appendLog(logPath, "data")
    const entries = store.listLogs("ws-1")
    store.deleteLog("ws-1", entries[0].filename)
    expect(store.listLogs("ws-1")).toHaveLength(0)
  })

  it("prunes logs older than maxAge (keeps recent)", () => {
    store.createLogFile("ws-1")
    const removed = store.prune(30)
    expect(removed).toBe(0)
    expect(store.listLogs("ws-1")).toHaveLength(1)
  })
})
