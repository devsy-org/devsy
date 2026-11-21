import { describe, expect, it } from "vitest"
import { sanitizeRendererErrorMessage } from "../../shared/renderer-error.js"

describe("renderer error message sanitization", () => {
  it("removes secrets, stacks, paths, and URLs while keeping useful text", () => {
    const message = sanitizeRendererErrorMessage(
      "Workspace load failed token=secret https://example.test/?secret=123 /Users/private/file.ts:1",
    )
    expect(message).toContain("Workspace load failed")
    for (const secret of [
      "secret",
      "example.test",
      "123",
      "/Users",
      "file.ts",
    ]) {
      expect(message).not.toContain(secret)
    }
  })

  it.each([
    [
      String.raw`Request failed token="hidden\"escaped-canary" retry`,
      "Request failed [redacted] retry",
    ],
    [
      String.raw`Request failed token='hidden\'escaped-canary' retry`,
      "Request failed [redacted] retry",
    ],
    [
      `Request failed password="prefix truncated-canary ${"x".repeat(4096)}"`,
      "Request failed [redacted]",
    ],
    [
      String.raw`Request failed token=\"prefix encoded-canary\" retry`,
      "Request failed [redacted]",
    ],
  ])("masks the whole quoted sensitive value", (message, expected) => {
    expect(sanitizeRendererErrorMessage(message)).toBe(expected)
  })

  it.each([
    "access_token=access-canary",
    "client_secret is client-canary",
    "private key: private-canary",
    'accessToken=["array-canary", {"nested": ["nested-canary"]}]',
    'token= access_token="prefix quoted-tail-canary"',
    "password is client_secret is prose-tail-canary",
    `${"token= ".repeat(256)}"access_token": ["prefix", "group-tail-canary"]`,
    "Authorization: token auth-canary",
    'pRoXy-aUtHoRiZaTiOn: Extension "first alternate-canary"',
    "Authorization: 'first single-canary'",
    "Authorization: Basic Y2FuYXJ5IQ==",
    "request.headers.authorization=Extension qualified-canary",
    "password=alpha,beta;gamma",
  ])("keeps compound credential values out of the message", (field) => {
    expect(
      sanitizeRendererErrorMessage(`Request failed ${field} workspace=staging`),
    ).toBe("Request failed [redacted] workspace=staging")
  })

  it.each([
    'client_secret={"nested": ["hidden"], "note": "remaining-canary"',
    'Authorization: Extension keyId="private identity",signature=signed-canary',
    'Authorization: Extension "complete" quoted-tail-canary',
    'Proxy-Authorization: Extension ["complete"] field=group-tail-canary workspace=staging',
    "Authorization: token auth-canary field=scalar-tail-canary workspace=staging",
    "Authorization: token auth-canary workspace=staging field=ambiguous-tail-canary",
  ])("masks an incomplete or opaque credential field", (field) => {
    expect(sanitizeRendererErrorMessage(`Request failed ${field}`)).toBe(
      "Request failed [redacted]",
    )
  })

  it.each([
    ["path", "prefix-canary/suffix-canary"],
    ["query", "prefix-canary?suffix-canary"],
    ["details", "prefix-canary{suffix-canary}"],
    ["url", "prefix-canary://suffix-canary"],
  ])(
    "masks a registered value before the %s replacement can split it",
    (_name, registered) => {
      const message = sanitizeRendererErrorMessage(
        `Workspace failed ${registered} retry`,
        [registered],
      )
      expect(message).toBe("Workspace failed [redacted] retry")
    },
  )

  it("masks a registered value before truncation can shorten it", () => {
    const registered = "registered-canary-value"
    const message = sanitizeRendererErrorMessage(
      `${"a".repeat(120)}${registered}`,
      [registered],
    )
    expect(message).not.toContain("regi")
  })

  it.each([
    ["token", "Failed token=private-canary retry"],
    ["secret", "Failed client_secret=private-canary retry"],
    ["password", "Failed password: private-canary retry"],
  ])(
    "masks the credential value when the registered value %s overlaps its label",
    (registered, message) => {
      const result = sanitizeRendererErrorMessage(message, [registered])
      expect(result).not.toContain("private-canary")
      expect(result).toContain("Failed")
      expect(result).toContain("retry")
    },
  )

  it.each([
    "password=private-canary",
    "password is private-canary",
    "client_secret=private-canary",
    "Authorization: token private-canary",
  ])(
    "scans credentials in the record that remains after a registered value joins records (%s)",
    (assignment) => {
      const registered = "first-canary\nsecond-canary"
      const result = sanitizeRendererErrorMessage(
        `Failed ${registered} ${assignment} retry`,
        [registered],
      )
      expect(result).not.toContain("private-canary")
      expect(result).not.toContain("first-canary")
      expect(result).toContain("Failed")
    },
  )

  it("masks a registered value that spans a record boundary", () => {
    const registered = "first-canary\nsecond-canary"
    const message = sanitizeRendererErrorMessage(
      `Workspace failed ${registered} retry`,
      [registered],
    )
    expect(message).toBe("Workspace failed [redacted] retry")
  })

  it("masks overlapping registered values as one range", () => {
    const message = sanitizeRendererErrorMessage("x abcdef y", ["abcd", "cdef"])
    expect(message).toBe("x [redacted] y")
  })

  it("masks a registered value at the start of the message", () => {
    expect(sanitizeRendererErrorMessage("abc rest", ["abc"])).toBe(
      "[redacted] rest",
    )
  })

  it("ignores empty registered values", () => {
    expect(sanitizeRendererErrorMessage("plain text", [""])).toBe("plain text")
  })
})
