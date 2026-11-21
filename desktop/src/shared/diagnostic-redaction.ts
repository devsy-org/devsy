const credentialKey =
  /(password|passwd|passphrase|token|secret|apikey|authorization|credential|privatekey|accesskey|telemetrydistinctid)/i
const compoundCredentialKey = new RegExp(`^${credentialKey.source}$`, "i")

function normalizeKey(key: string): string {
  return key.replace(/[^a-z0-9]/gi, "").toLowerCase()
}

export function isCredentialKey(key: string): boolean {
  return credentialKey.test(normalizeKey(key))
}

export function* credentialAssignments(value: string, allowProse = false) {
  const tokens = /[a-z0-9_.-]+/gi
  for (let match = tokens.exec(value); match; match = tokens.exec(value)) {
    const assignment = credentialAssignment(value, match, allowProse)
    if (!assignment) continue
    const { start, authorizationStart } = credentialContent(
      value,
      assignment,
      allowProse,
    )
    const end = credentialValueEnd(value, start, authorizationStart)
    if (end === assignment.start) continue
    yield { keyStart: assignment.keyStart, start: assignment.start, end }
    tokens.lastIndex = end
  }
}

function credentialAssignment(
  value: string,
  match: RegExpExecArray,
  allowProse: boolean,
) {
  const { index } = match
  const keyEnd = compoundKeyEnd(value, index, index + match[0].length)
  const key = normalizeKey(value.slice(index, keyEnd))
  if (!credentialKey.test(key)) return
  const start = assignmentValueStart(value, keyEnd, allowProse)
  if (start < 0) return
  const quote = value[index - 1]
  const keyStart = isQuote(quote) && value[keyEnd] === quote ? index - 1 : index
  const authorization = key === "authorization" || key === "proxyauthorization"
  return {
    keyStart,
    start,
    authorizationStart: authorization ? keyStart : undefined,
  }
}

function credentialContent(
  value: string,
  initial: { start: number; authorizationStart: number | undefined },
  allowProse: boolean,
) {
  const tokens = /[a-z0-9_.-]+/iy
  let { start, authorizationStart } = initial
  while (start < value.length) {
    const authorization = authorizationStart !== undefined
    start = schemeContentStart(value, start, authorization)
    if (authorization) break
    tokens.lastIndex = start + Number(isQuote(value[start]))
    const match = tokens.exec(value)
    if (!match) break
    const assignment = credentialAssignment(value, match, allowProse)
    if (!assignment || assignment.keyStart !== start) break
    start = assignment.start
    authorizationStart = assignment.authorizationStart
  }
  return { start, authorizationStart }
}

function compoundKeyEnd(value: string, start: number, end: number): number {
  const suffix = /[ \t]+[a-z0-9_.-]+/iy
  suffix.lastIndex = end
  for (let part = 0; part < 2 && suffix.exec(value); part++) {
    if (
      compoundCredentialKey.test(
        normalizeKey(value.slice(start, suffix.lastIndex)),
      )
    ) {
      end = suffix.lastIndex
    }
  }
  return end
}

function isQuote(value: string | undefined): boolean {
  return value === '"' || value === "'"
}

function assignmentValueStart(
  value: string,
  keyEnd: number,
  allowProse: boolean,
): number {
  const separator = skipWhitespace(
    value,
    keyEnd + Number(isQuote(value[keyEnd])),
  )
  if (value[separator] === ":" || value[separator] === "=")
    return skipWhitespace(value, separator + 1)
  if (allowProse && /^is\b/i.test(value.slice(separator, separator + 3)))
    return skipWhitespace(value, separator + 2)
  return -1
}

function skipWhitespace(value: string, next: number): number {
  while (next < value.length && /\s/.test(value[next])) next++
  return next
}

function schemeContentStart(
  value: string,
  start: number,
  authorization: boolean,
): number {
  if (isQuote(value[start])) return start
  const scheme = /[!#$%&'*+.^_`|~a-z0-9-]+/iy
  scheme.lastIndex = start
  const match = scheme.exec(value)
  if (
    !match ||
    !/\s/.test(value[scheme.lastIndex] ?? "") ||
    (!authorization && !/^(?:basic|bearer)$/i.test(match[0]))
  )
    return start
  return skipWhitespace(value, scheme.lastIndex)
}

function credentialValueEnd(
  value: string,
  start: number,
  authorizationStart: number | undefined,
): number {
  let opener = start
  while (value[opener] === "\\") opener++
  const first = value[opener]
  if (isQuote(first) || first === "[" || first === "{") {
    if (opener !== start) return value.length
    const end = isQuote(first)
      ? quotedValueEnd(value, start)
      : structuredValueEnd(value, start)
    return authorizationStart === undefined
      ? end
      : authorizationValueEnd(value, authorizationStart, end)
  }
  let next = start
  while (next < value.length && !/[\s,;]/.test(value[next])) next++
  if (authorizationStart === undefined) return next
  return /^[a-z0-9._~+/-]+=*$/i.test(value.slice(start, next))
    ? authorizationValueEnd(value, authorizationStart, next)
    : value.length
}

function authorizationValueEnd(
  value: string,
  start: number,
  end: number,
): number {
  if (skipWhitespace(value, end) === value.length) return end
  const field = /[ \t]+workspace=[a-z0-9][a-z0-9._-]*\s*$/iy
  field.lastIndex = end
  return field.test(value) &&
    !/[\r\n\u2028\u2029]/.test(value.slice(start, end))
    ? end
    : value.length
}

function quotedValueEnd(value: string, start: number): number {
  const quote = value[start]
  let next = start + 1
  while (next < value.length) {
    if (value[next] === "\\") {
      next = Math.min(next + 2, value.length)
    } else if (value[next++] === quote) {
      break
    }
  }
  return next
}

function structuredValueEnd(value: string, start: number): number {
  const closing: string[] = []
  for (let next = start; next < value.length; next++) {
    const character = value[next]
    if (isQuote(character)) {
      next = quotedValueEnd(value, next) - 1
    } else if (character === "[" || character === "{") {
      closing.push(character === "[" ? "]" : "}")
    } else if (character === "]" || character === "}") {
      if (closing.pop() !== character) return value.length
      if (closing.length === 0) return next + 1
    }
  }
  return value.length
}
