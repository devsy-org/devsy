import assert from "node:assert/strict"
import { test } from "node:test"
import {
  credentialAssignments,
  isCredentialKey,
} from "./diagnostic-redaction.ts"

function assertAssignment(
  label: string,
  body: string,
  suffix = "",
  allowProse = false,
): void {
  const prefix = "Request failed "
  const value = prefix + label + body + suffix
  const start = prefix.length + label.length
  assert.deepEqual(
    [...credentialAssignments(value, allowProse)],
    [{ keyStart: prefix.length, start, end: start + body.length }],
  )
}

const workspace = " workspace=staging"

test("applies authorization scanning to the terminal segment of a qualified key", () => {
  for (const key of [
    "request.headers.authorization",
    "response.Headers.Proxy-Authorization",
    '"telemetry.request.PROXY_AUTHORIZATION"',
  ]) {
    assertAssignment(`${key}=`, "Extension qualified-canary", workspace)
  }
  assertAssignment(
    "request.headers.authorizationMode=",
    "mode-canary",
    " diagnostic=ordinary",
  )
})

test("includes embedded and trailing punctuation in unquoted values", () => {
  assertAssignment("password=", "alpha,beta;gamma", workspace)
  assertAssignment("client_secret=", ",;punctuation-canary;,", workspace)
  assertAssignment("access_token=", "unseparated-canary;workspace=staging")
  assertAssignment("Authorization: ", `token auth-canary;${workspace}`)
})

test("keeps complete quoted, grouped and chained credential boundaries", () => {
  assertAssignment("token=", '"prefix \\"quoted-canary"', workspace)
  assertAssignment(
    "client_secret=",
    '["first", {"nested": ["canary"]}]',
    workspace,
  )
  assertAssignment("private key: ", "scalar-canary", workspace)
  assertAssignment(
    "token= ",
    'access_token="prefix quoted-tail-canary"',
    workspace,
  )
  assertAssignment(
    "password is ",
    "client_secret is prose-canary",
    workspace,
    true,
  )
  assertAssignment(
    "token= ",
    `${"token= ".repeat(255)}"access_token": ["prefix", "group-tail-canary"]`,
    workspace,
  )
})

test("preserves an ordinary workspace field after a complete same-line authorization value", () => {
  assertAssignment("Authorization: ", "token auth-canary", workspace)
  assertAssignment("Authorization: ", "Basic Y2FuYXJ5IQ==", workspace)
  assertAssignment("Authorization: ", "'first single-canary'", workspace)
  assertAssignment(
    "pRoXy-aUtHoRiZaTiOn: ",
    'Extension "first canary"',
    workspace,
  )
  assertAssignment(
    "Proxy-Authorization: ",
    'Extension ["first", "canary"]',
    workspace,
  )
})

test("masks the remainder when a credential boundary is ambiguous", () => {
  assertAssignment("token=", '\\"prefix encoded-canary\\" retry')
  assertAssignment("client_secret=", '{"nested": ["hidden"], "note": "canary"')
  assertAssignment(
    "Authorization: ",
    'Extension keyId="first",signature=canary',
  )
  assertAssignment("Authorization: ", 'Extension "first" tail-canary')
  assertAssignment(
    "Proxy-Authorization: ",
    `Extension ["first"] next=canary${workspace}`,
  )
  assertAssignment("Authorization: ", `token first${workspace} next=canary`)
  assertAssignment(
    "Authorization: ",
    "Basic Y2FuYXJ5IQ==\r\n workspace=folded-canary",
  )
  assertAssignment(
    "Proxy-Authorization: ",
    'Extension "first"\n\tworkspace=folded-canary',
  )
  assertAssignment(
    "Authorization:\r\n ",
    "Bearer first workspace=folded-canary",
  )
  assertAssignment(
    "Authorization: ",
    "Bearer\r\n first workspace=folded-canary",
  )
  assertAssignment(
    "Proxy-Authorization: ",
    'Extension "first\u2028second" workspace=folded-canary',
  )
})

test("classifies structured credential keys without classifying ordinary fields", () => {
  for (const key of [
    "accessToken",
    "client_secret",
    "private key",
    "request.headers.authorization",
  ]) {
    assert.equal(isCredentialKey(key), true)
  }
  assert.equal(isCredentialKey("workspace"), false)
  assert.deepEqual([...credentialAssignments("workspace=staging")], [])
})

test("keeps generated credential spans complete and workspace suffixes intact", () => {
  const punctuation = [",", ";", ",;", ";,", ",,;;"]
  const keys = ["password", "CLIENT_SECRET", "access-token", "private key"]
  const authorizations = [
    "authorization",
    "AUTHORIZATION",
    "proxy-authorization",
  ]
  for (const seed of [1, 17, 0x49a2, 0xc0ffee]) {
    let state = seed
    for (let iteration = 0; iteration < 32; iteration++) {
      state = (Math.imul(state, 1664525) + 1013904223) >>> 0
      const marker = `${String(seed)}-${String(iteration)}`
      const separator = punctuation[state % punctuation.length]
      const key = keys[(state >>> 4) % keys.length]
      assertAssignment(
        `${key}=`,
        `first-${marker}${separator}last-${marker}`,
        workspace,
      )
      const header = authorizations[(state >>> 8) % authorizations.length]
      const qualified = ["request", `trace_${marker}`, "headers", header].join(
        ".",
      )
      assertAssignment(
        `${qualified}=`,
        `Extension credential-${marker}`,
        workspace,
      )
    }
  }
})
