"use strict"

const fs = require("node:fs")

function withFileLock(lockPath, update) {
  const waitCell = new Int32Array(new SharedArrayBuffer(4))
  let descriptor

  while (descriptor === undefined) {
    try {
      descriptor = fs.openSync(lockPath, "wx")
    } catch (error) {
      if (error.code !== "EEXIST") throw error
      if (removeStaleLock(lockPath)) continue
      Atomics.wait(waitCell, 0, 0, 10)
    }
  }

  try {
    fs.writeSync(descriptor, String(process.pid))
    return update()
  } finally {
    fs.closeSync(descriptor)
    fs.unlinkSync(lockPath)
  }
}

function removeStaleLock(lockPath) {
  let contents
  let stat
  try {
    contents = fs.readFileSync(lockPath, "utf8")
    stat = fs.statSync(lockPath)
  } catch (error) {
    return error.code === "ENOENT"
  }

  const owner = Number(contents)
  if (Number.isInteger(owner) && owner > 0) {
    try {
      process.kill(owner, 0)
      return false
    } catch (error) {
      if (error.code !== "ESRCH") return false
    }
  } else if (Date.now() - stat.mtimeMs < 1000) {
    return false
  }

  try {
    const latest = fs.statSync(lockPath)
    if (latest.dev !== stat.dev || latest.ino !== stat.ino) return false
    fs.unlinkSync(lockPath)
    return true
  } catch (error) {
    return error.code === "ENOENT"
  }
}

function mergeTasks(current, update, deletedIds = [], tombstones = {}) {
  const deleted = new Set([...Object.keys(tombstones), ...deletedIds])
  const tasks = { ...current, ...update }
  for (const id of deleted) delete tasks[id]

  for (const [id, task] of Object.entries(current)) {
    const next = update[id]
    if (
      !deleted.has(id) &&
      next &&
      task.status !== "pending" &&
      task.status !== next.status
    ) {
      tasks[id] = task
    }
  }

  return tasks
}

module.exports = { mergeTasks, withFileLock }
