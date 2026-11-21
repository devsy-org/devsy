const credentialKey =
  /(password|passwd|passphrase|token|secret|apikey|authorization|credential|privatekey|accesskey|telemetrydistinctid)/i
const compoundCredentialKey = new RegExp(`^${credentialKey.source}$`, "i")

function normalizeKey(key: string): string {
  return key.replace(/[^a-z0-9]/gi, "")
}

export function isCredentialKey(key: string): boolean {
  return credentialKey.test(normalizeKey(key))
}

export function* credentialAssignments(value: string, allowProse = false) {
  const tokens = /[a-z0-9_.-]+/gi
  for (let match = tokens.exec(value); match; match = tokens.exec(value)) {
    const assignment = credentialAssignment(value, match, allowProse)
    if (!assignment) continue
    const end = credentialValueEnd(
      value,
      credentialContentStart(value, assignment.start, allowProse),
    )
    if (end === assignment.start) continue
    yield { ...assignment, end }
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
  if (!isCredentialKey(value.slice(index, keyEnd))) return
  const start = assignmentValueStart(value, keyEnd, allowProse)
  if (start < 0) return
  const quote = value[index - 1]
  const keyStart = isQuote(quote) && value[keyEnd] === quote ? index - 1 : index
  return { keyStart, start }
}

function credentialContentStart(
  value: string,
  start: number,
  allowProse: boolean,
): number {
  const tokens = /[a-z0-9_.-]+/iy
  let content = authorizationContentStart(value, start)
  while (content < value.length) {
    tokens.lastIndex = content + Number(isQuote(value[content]))
    const match = tokens.exec(value)
    if (!match) break
    const assignment = credentialAssignment(value, match, allowProse)
    if (!assignment || assignment.keyStart !== content) break
    content = authorizationContentStart(value, assignment.start)
  }
  return content
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

function authorizationContentStart(value: string, start: number): number {
  const scheme = value.slice(start, start + 6).toLowerCase()
  const schemeLength = scheme.startsWith("bearer")
    ? 6
    : scheme.startsWith("basic")
      ? 5
      : 0
  if (schemeLength && /\s/.test(value[start + schemeLength] ?? ""))
    return skipWhitespace(value, start + schemeLength)
  return start
}

function credentialValueEnd(value: string, start: number): number {
  let opener = start
  while (value[opener] === "\\") opener++
  const first = value[opener]
  if (isQuote(first) || first === "[" || first === "{") {
    if (opener !== start) return value.length
    return isQuote(first)
      ? quotedValueEnd(value, start)
      : structuredValueEnd(value, start)
  }
  let next = start
  while (next < value.length && !/[\s,;]/.test(value[next])) next++
  return next
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
