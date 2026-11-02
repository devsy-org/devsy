import { readFileSync } from "node:fs"
import { join } from "node:path"
import { describe, expect, it } from "vitest"
import {
  parseIndexResponse,
  parseReadResponse,
} from "../../shared/workspace-logs.js"

// The fixtures are written by the Go test TestGoldenResponsesMatchDesktopFixtures
// from real CLI output, so a field rename on either side fails here.
function fixture(name: string): unknown {
  return JSON.parse(readFileSync(join(__dirname, "fixtures", name), "utf8"))
}

describe("Go workspace log responses", () => {
  it("parses a real read response", () => {
    const response = parseReadResponse(fixture("go-read-response.json"))

    expect(response.complete).toBe(true)
    expect(response.hasMore).toBe(false)
    const output = response.records.filter((r) => r.kind === "output")
    expect(output.map((r) => (r.kind === "output" ? r.text : ""))).toEqual([
      "out-1\n",
      "err-1\n",
      "out-2\n",
    ])
    const states = response.records
      .filter((r) => r.kind === "invocation")
      .map((r) => (r.kind === "invocation" ? r.state : ""))
    expect(states).toContain("failed")
    expect(states).toContain("succeeded")
  })

  it("parses a real index response", () => {
    const response = parseIndexResponse(fixture("go-index-response.json"))

    expect(response.runs).toHaveLength(1)
    expect(response.runs[0].commandState).toBeTruthy()
    expect(response.hasMore).toBe(false)
  })
})
