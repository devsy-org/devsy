/** Stable identity even when contexts or names contain separators. */
export function variableIdentity(context: string, name: string): string {
  return JSON.stringify([context, name])
}
