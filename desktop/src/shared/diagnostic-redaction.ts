export function skipWhitespace(value: string, next: number): number {
  while (next < value.length && /\s/.test(value[next])) next++
  return next
}

export function credentialValueEnd(value: string, start: number): number {
  let next = start
  const scheme = value.slice(next, next + 6).toLowerCase()
  const schemeLength = scheme.startsWith("bearer")
    ? 6
    : scheme.startsWith("basic")
      ? 5
      : 0
  if (schemeLength && /\s/.test(value[next + schemeLength] ?? "")) {
    next = skipWhitespace(value, next + schemeLength)
  }
  let quoteStart = next
  while (value[quoteStart] === "\\") quoteStart++
  const quote = value[quoteStart]
  if (quote !== '"' && quote !== "'") {
    while (next < value.length && !/[\s,;]/.test(value[next])) next++
    return next
  }
  if (quoteStart !== next) return value.length
  next++
  while (next < value.length) {
    if (value[next] === "\\") {
      next = Math.min(next + 2, value.length)
    } else if (value[next++] === quote) {
      break
    }
  }
  return next
}
