"use strict"

function mergeTasks(current, update, deletedIds = []) {
  const tasks = { ...current, ...update }
  for (const id of deletedIds) delete tasks[id]

  for (const [id, task] of Object.entries(current)) {
    const next = update[id]
    if (next && task.status !== "pending" && task.status !== next.status) {
      tasks[id] = task
    }
  }

  return tasks
}

module.exports = { mergeTasks }
