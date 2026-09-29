import type { WorkspaceStatus } from "$lib/types/index.js"

export type LaunchOperationState = "running" | "succeeded" | "failed" | "skipped"

export interface LaunchOperation {
  key: string
  operationId?: string
  parentOperationId?: string
  phase: string
  step?: string
  state: LaunchOperationState
  firstSeenAtMs: number
  durationMs?: number
  error?: WorkspaceStatus["error"]
}

export interface WorkspaceLaunchTimeline {
  operations: LaunchOperation[]
}

export function createWorkspaceLaunchTimeline(): WorkspaceLaunchTimeline {
  return { operations: [] }
}

function findOperationIndex(
  operations: LaunchOperation[],
  event: WorkspaceStatus,
  state: LaunchOperationState,
): number {
  if (event.operationId) {
    return operations.findIndex((operation) => operation.operationId === event.operationId)
  }

  for (let index = operations.length - 1; index >= 0; index -= 1) {
    const operation = operations[index]
    if (operation.phase === event.phase && operation.step === event.step) {
      if (operation.state === "running" || operation.state === state) return index
    }
  }

  return -1
}

function createOperation(
  event: WorkspaceStatus,
  state: LaunchOperationState,
  key: string,
  nowMs: number,
): LaunchOperation {
  return {
    key,
    operationId: event.operationId,
    parentOperationId: event.parentOperationId,
    phase: event.phase,
    step: event.step,
    state,
    firstSeenAtMs: nowMs,
    durationMs: event.durationMs,
    error: event.error,
  }
}

function updateOperation(
  existing: LaunchOperation,
  event: WorkspaceStatus,
  state: LaunchOperationState,
): LaunchOperation {
  return {
    ...existing,
    operationId: event.operationId ?? existing.operationId,
    parentOperationId: event.parentOperationId ?? existing.parentOperationId,
    phase: event.phase,
    step: event.step ?? existing.step,
    state: state === "running" && existing.state !== "running" ? existing.state : state,
    durationMs: event.durationMs ?? existing.durationMs,
    error: event.error ?? existing.error,
  }
}

export function reduceWorkspaceLaunchTimeline(
  timeline: WorkspaceLaunchTimeline,
  event: WorkspaceStatus,
  nowMs: number,
): WorkspaceLaunchTimeline {
  const operations = [...timeline.operations]
  const state: LaunchOperationState = event.state === "started" ? "running" : event.state
  const index = findOperationIndex(operations, event, state)

  if (index < 0) {
    operations.push(createOperation(event, state, event.operationId ?? `synthetic-${operations.length + 1}`, nowMs))
    return { operations }
  }

  operations[index] = updateOperation(operations[index], event, state)
  return { operations }
}
