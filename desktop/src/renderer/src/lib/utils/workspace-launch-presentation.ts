import type {
  LaunchOperation,
  WorkspaceLaunchTimeline,
} from "./workspace-launch-timeline.js"

interface RankedOperation {
  operation: LaunchOperation
  depth: number
  index: number
}

export type LaunchConfirmationState =
  | "running"
  | "confirming"
  | "stale"
  | "confirmed"
  | "failed"

export interface LaunchOperationPresentation {
  descendants: Array<{ operation: LaunchOperation; depth: number }>
  detail?: LaunchOperation
  hasFailedDescendant: boolean
  errorMessage?: string
}

function descendantsOf(
  parent: LaunchOperation,
  timeline: WorkspaceLaunchTimeline,
): RankedOperation[] {
  if (!parent.operationId) return []

  const childrenByParent = new Map<string, RankedOperation[]>()
  timeline.operations.forEach((operation, index) => {
    if (!operation.parentOperationId) return
    const children = childrenByParent.get(operation.parentOperationId) ?? []
    children.push({ operation, depth: 0, index })
    childrenByParent.set(operation.parentOperationId, children)
  })

  const descendants: RankedOperation[] = []
  const visited = new Set([parent.operationId])

  function visit(parentId: string, depth: number) {
    for (const child of childrenByParent.get(parentId) ?? []) {
      const operationId = child.operation.operationId
      if (operationId && visited.has(operationId)) continue
      const ranked = { ...child, depth }
      descendants.push(ranked)
      if (operationId) {
        visited.add(operationId)
        visit(operationId, depth + 1)
      }
    }
  }

  visit(parent.operationId, 1)
  return descendants
}

function deepestMostRecent(
  operations: RankedOperation[],
): RankedOperation | undefined {
  return operations.reduce<RankedOperation | undefined>((best, candidate) => {
    if (!best || candidate.depth > best.depth) return candidate
    if (candidate.depth === best.depth && candidate.index > best.index) {
      return candidate
    }
    return best
  }, undefined)
}

export function topLevelLaunchOperations(
  timeline: WorkspaceLaunchTimeline,
): LaunchOperation[] {
  const operationIds = new Set(
    timeline.operations
      .map((operation) => operation.operationId)
      .filter((operationId): operationId is string => Boolean(operationId)),
  )
  return timeline.operations.filter(
    (operation) =>
      !operation.parentOperationId ||
      !operationIds.has(operation.parentOperationId),
  )
}

export function presentLaunchOperation(
  operation: LaunchOperation,
  timeline: WorkspaceLaunchTimeline,
  launchError?: string,
): LaunchOperationPresentation {
  const descendants = descendantsOf(operation, timeline)
  const failed = descendants.filter(
    ({ operation: child }) => child.state === "failed",
  )
  const running = descendants.filter(
    ({ operation: child }) => child.state === "running",
  )
  const failure = deepestMostRecent(failed)
  const detail = failure ?? deepestMostRecent(running)

  return {
    descendants: descendants.map(({ operation: child, depth }) => ({
      operation: child,
      depth,
    })),
    detail: detail?.operation,
    hasFailedDescendant: failure !== undefined,
    errorMessage:
      failure?.operation.error?.message ??
      operation.error?.message ??
      launchError,
  }
}
