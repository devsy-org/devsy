export const SSH_HOST_SUFFIX = ".devsy"
export const DISPLAY_LABEL_MAX_LENGTH = 120
export const SSH_ALIAS_MAX_LENGTH = 48

export type WorkspacePresentationField =
  | "displayName"
  | "sshHostAlias"
  | "workspace"

export type WorkspacePresentationErrorCode =
  | "invalid"
  | "conflict"
  | "stale"
  | "managed"
  | "not-found"
  | "failed"

export interface WorkspacePresentationRequest {
  workspaceId: string
  expectedUid: string
  context?: string
  displayName?: string
  sshHostAlias?: string
  expectedDisplayName?: string
  expectedSshHostAlias?: string
}

export interface WorkspacePresentationResult {
  id: string
  uid: string
  context: string
  displayName: string
  sshHostAlias: string
  effectiveDisplayName: string
  preferredSSHHost: string
  canonicalSSHHost: string
  sshEntryUpdated: boolean
}

export interface WorkspacePresentationFailure {
  field: WorkspacePresentationField
  code: WorkspacePresentationErrorCode
  message: string
  conflict?: string
}

export type WorkspacePresentationReply =
  | { ok: true; result: WorkspacePresentationResult }
  | ({ ok: false } & WorkspacePresentationFailure)

export function normalizeDisplayLabel(value: string): string {
  return value.trim().normalize("NFC")
}

export function validateDisplayLabel(value: string): string | null {
  const label = normalizeDisplayLabel(value)
  if (label === "") return null
  if (/\p{Cc}/u.test(label)) {
    return "Display label cannot contain control characters."
  }
  if ([...label].length > DISPLAY_LABEL_MAX_LENGTH) {
    return `Display label must be at most ${DISPLAY_LABEL_MAX_LENGTH} characters.`
  }
  return null
}

export function validateSshAlias(value: string): string | null {
  const alias = value.trim()
  if (alias === "") return null
  if (alias.toLowerCase().endsWith(SSH_HOST_SUFFIX)) {
    return `Enter the alias without the ${SSH_HOST_SUFFIX} suffix.`
  }
  if (/\s/.test(alias)) return "SSH alias cannot contain spaces."
  if (/[./\\*?!]/.test(alias)) {
    return "SSH alias cannot contain dots, slashes, or wildcards."
  }
  if (alias.length > SSH_ALIAS_MAX_LENGTH) {
    return `SSH alias must be at most ${SSH_ALIAS_MAX_LENGTH} characters.`
  }
  if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(alias)) {
    return "Use lowercase letters, numbers, and dashes; start and end with a letter or number."
  }
  return null
}

function invalidRequest(message: string): WorkspacePresentationFailure {
  return { field: "workspace", code: "invalid", message }
}

function validateRequestStrings(
  request: WorkspacePresentationRequest,
): WorkspacePresentationFailure | null {
  const strings: Array<[unknown, boolean]> = [
    [request.workspaceId, true],
    [request.expectedUid, true],
    [request.context, false],
    [request.displayName, false],
    [request.sshHostAlias, false],
    [request.expectedDisplayName, false],
    [request.expectedSshHostAlias, false],
  ]
  for (const [value, required] of strings) {
    const missing = value === undefined || value === ""
    if (missing && required) {
      return invalidRequest("Workspace identity is required to save changes.")
    }
    if (value !== undefined && typeof value !== "string") {
      return invalidRequest("Invalid workspace edit request.")
    }
  }
  return null
}

function validateRequestExpectations(
  request: WorkspacePresentationRequest,
): WorkspacePresentationFailure | null {
  if (request.displayName === undefined && request.sshHostAlias === undefined) {
    return invalidRequest("Nothing to save.")
  }
  const orphanExpectation =
    (request.displayName === undefined &&
      request.expectedDisplayName !== undefined) ||
    (request.sshHostAlias === undefined &&
      request.expectedSshHostAlias !== undefined)
  return orphanExpectation
    ? invalidRequest("Invalid workspace edit request.")
    : null
}

function validateRequestFields(
  request: WorkspacePresentationRequest,
): WorkspacePresentationFailure | null {
  if (request.displayName !== undefined) {
    const message = validateDisplayLabel(request.displayName)
    if (message) return { field: "displayName", code: "invalid", message }
  }
  if (request.sshHostAlias !== undefined) {
    const message = validateSshAlias(request.sshHostAlias)
    if (message) return { field: "sshHostAlias", code: "invalid", message }
  }
  return null
}

export function buildWorkspaceUpPresentationArgs(args: {
  displayName?: string
  sshHostAlias?: string
}): string[] {
  const result: string[] = []
  const displayName = args.displayName?.trim()
  if (displayName) {
    const invalid = validateDisplayLabel(displayName)
    if (invalid) throw new Error(invalid)
    result.push("--display-name", normalizeDisplayLabel(displayName))
  }
  const sshHostAlias = args.sshHostAlias?.trim()
  if (sshHostAlias) {
    const invalid = validateSshAlias(sshHostAlias)
    if (invalid) throw new Error(invalid)
    result.push("--ssh-host-alias", sshHostAlias)
  }
  return result
}

export function validateWorkspacePresentationRequest(
  request: WorkspacePresentationRequest,
): WorkspacePresentationFailure | null {
  if (!request || typeof request !== "object") {
    return invalidRequest("Invalid workspace edit request.")
  }
  return (
    validateRequestStrings(request) ??
    validateRequestExpectations(request) ??
    validateRequestFields(request)
  )
}

export interface WorkspacePresentationBase {
  id: string
  uid: string
  context: string
  displayName: string
  sshHostAlias: string
}

export interface WorkspacePresentationDrafts {
  displayName: string
  sshHostAlias: string
}

export function buildWorkspacePresentationRequest(
  base: WorkspacePresentationBase,
  drafts: WorkspacePresentationDrafts,
): WorkspacePresentationRequest | null {
  const request: WorkspacePresentationRequest = {
    workspaceId: base.id,
    expectedUid: base.uid,
    context: base.context,
  }
  if (
    normalizeDisplayLabel(drafts.displayName) !==
    normalizeDisplayLabel(base.displayName)
  ) {
    request.displayName = normalizeDisplayLabel(drafts.displayName)
    request.expectedDisplayName = base.displayName
  }
  if (drafts.sshHostAlias.trim() !== base.sshHostAlias.trim()) {
    request.sshHostAlias = drafts.sshHostAlias.trim()
    request.expectedSshHostAlias = base.sshHostAlias
  }
  if (request.displayName === undefined && request.sshHostAlias === undefined) {
    return null
  }
  return request
}

export function buildWorkspaceEditArgs(
  request: WorkspacePresentationRequest,
  context: string,
): string[] {
  const args = [
    "--context",
    context,
    "workspace",
    "edit",
    request.workspaceId,
    "--expected-uid",
    request.expectedUid,
  ]
  if (request.displayName !== undefined) {
    args.push("--display-name", normalizeDisplayLabel(request.displayName))
    if (request.expectedDisplayName !== undefined) {
      args.push("--expected-display-name", request.expectedDisplayName)
    }
  }
  if (request.sshHostAlias !== undefined) {
    args.push("--ssh-host-alias", request.sshHostAlias.trim())
    if (request.expectedSshHostAlias !== undefined) {
      args.push("--expected-ssh-host-alias", request.expectedSshHostAlias)
    }
  }
  args.push("--result-format", "json")
  return args
}

const FAILURE_CODES: Record<string, true> = {
  invalid: true,
  conflict: true,
  stale: true,
  managed: true,
  "not-found": true,
}

const FIELDS: Record<string, true> = {
  displayName: true,
  sshHostAlias: true,
  workspace: true,
}

export function presentationFailureFromCliError(
  cliError: { code?: string; message?: string; context?: unknown } | undefined,
  fallbackMessage: string,
): WorkspacePresentationFailure {
  const context =
    cliError?.context && typeof cliError.context === "object"
      ? (cliError.context as Record<string, unknown>)
      : {}
  const code =
    cliError?.code && Object.hasOwn(FAILURE_CODES, cliError.code)
      ? (cliError.code as WorkspacePresentationErrorCode)
      : "failed"
  const field =
    typeof context.field === "string" && Object.hasOwn(FIELDS, context.field)
      ? (context.field as WorkspacePresentationField)
      : "workspace"
  const conflict =
    typeof context.conflict === "string" && context.conflict !== ""
      ? context.conflict
      : undefined
  return {
    field,
    code,
    message: cliError?.message || fallbackMessage,
    ...(conflict ? { conflict } : {}),
  }
}

export function parseWorkspacePresentationResult(
  value: unknown,
): WorkspacePresentationResult | null {
  if (!value || typeof value !== "object" || Array.isArray(value)) return null
  const raw = value as Record<string, unknown>
  const text = (key: string): string | null =>
    typeof raw[key] === "string" ? (raw[key] as string) : null
  const id = text("id")
  const uid = text("uid")
  const context = text("context")
  const preferred = text("preferredSSHHost")
  const canonical = text("canonicalSSHHost")
  if (!id || !uid || !context || !preferred || !canonical) return null
  return {
    id,
    uid,
    context,
    displayName: text("displayName") ?? "",
    sshHostAlias: text("sshHostAlias") ?? "",
    effectiveDisplayName: text("effectiveDisplayName") ?? id,
    preferredSSHHost: preferred,
    canonicalSSHHost: canonical,
    sshEntryUpdated: raw.sshEntryUpdated === true,
  }
}
