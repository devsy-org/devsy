import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { after, describe, it } from "node:test"
import { fileURLToPath } from "node:url"

const script = fileURLToPath(new URL("./contrast.mjs", import.meta.url))
const source = readFileSync(
  fileURLToPath(new URL("./tokens.css", import.meta.url)),
  "utf8",
)
const dir = mkdtempSync(join(tmpdir(), "tokens-"))
let count = 0

after(() => rmSync(dir, { recursive: true, force: true }))

function run(css) {
  const file = join(dir, `tokens-${count++}.css`)
  writeFileSync(file, css)
  const r = spawnSync(process.execPath, [script, file], { encoding: "utf8" })
  return { code: r.status, out: r.stdout, err: r.stderr }
}

function mutate(from, to) {
  assert.ok(source.includes(from), `fixture lacks ${from}`)
  return source.replace(from, to)
}

function inDark(edit) {
  const at = source.indexOf(".dark {")
  return source.slice(0, at) + edit(source.slice(at))
}

describe("contrast.mjs", () => {
  it("passes on the shipped tokens", () => {
    const r = run(source)
    assert.equal(r.code, 0, r.out + r.err)
    assert.match(r.out, /:root\s+--duration-fast ok/)
  })

  it("rejects alpha in a :root colour", () => {
    const r = run(
      mutate(
        "--foreground: oklch(0.22 0.02 278)",
        "--foreground: oklch(0.22 0.02 278 / 0.5)",
      ),
    )
    assert.notEqual(r.code, 0)
    assert.match(r.err, /--foreground: alpha is not supported/)
  })

  it("rejects alpha in a .dark colour", () => {
    const r = run(
      inDark((s) =>
        s.replace(
          "--foreground: oklch(0.95 0.004 275)",
          "--foreground: oklch(0.95 0.004 275 / 0.5)",
        ),
      ),
    )
    assert.notEqual(r.code, 0)
    assert.match(r.err, /--foreground: alpha is not supported/)
  })

  it("rejects an oklch value with the wrong number of parts", () => {
    const r = run(
      mutate(
        "--foreground: oklch(0.22 0.02 278)",
        "--foreground: oklch(0.22 0.02)",
      ),
    )
    assert.notEqual(r.code, 0)
    assert.match(r.err, /--foreground: cannot read oklch\(0\.22 0\.02\)/)
  })

  it("rejects a non-numeric oklch value", () => {
    const r = run(
      mutate(
        "--foreground: oklch(0.22 0.02 278)",
        "--foreground: oklch(0.22 x 278)",
      ),
    )
    assert.notEqual(r.code, 0)
    assert.match(r.err, /--foreground: cannot read/)
  })

  it("rejects a var() alias in .dark", () => {
    const r = run(
      inDark((s) =>
        s.replace(
          "--card: oklch(",
          "--card: var(--background); --unused: oklch(",
        ),
      ),
    )
    assert.notEqual(r.code, 0)
    assert.match(
      r.err,
      /\.dark --card: var\(--background\) is not an oklch color/,
    )
  })

  it("rejects a hex colour in .dark", () => {
    const r = run(
      inDark((s) =>
        s.replace(
          "--foreground: oklch(0.95 0.004 275)",
          "--foreground: #f4f4f6",
        ),
      ),
    )
    assert.notEqual(r.code, 0)
    assert.match(r.err, /\.dark --foreground: #f4f4f6 is not an oklch color/)
  })

  it("fails when the file has no .dark block", () => {
    const r = run(source.slice(0, source.indexOf(".dark {")))
    assert.notEqual(r.code, 0)
    assert.match(r.err, /no \.dark block in tokens file/)
  })

  it("fails when a checked token is missing", () => {
    const r = run(source.replaceAll("--sidebar-ring:", "--sidebar-ringx:"))
    assert.notEqual(r.code, 0)
    assert.match(r.err, /missing token in :root: sidebar-ring or sidebar/)
  })

  it("exits 1 and marks FAIL when a pair drops under its minimum", () => {
    const r = run(
      mutate(
        "--muted-foreground: oklch(0.47 0.018 278)",
        "--muted-foreground: oklch(0.8 0.018 278)",
      ),
    )
    assert.equal(r.code, 1)
    assert.match(r.out, /muted-foreground\s+on background\s+[\d.]+ FAIL < 4\.5/)
  })

  it("requires the exact 150ms and 200ms duration strings", () => {
    for (const [from, to] of [
      ["--duration-fast: 150ms", "--duration-fast: 0.15s"],
      ["--duration-panel: 200ms", "--duration-panel: 250ms"],
    ]) {
      const r = run(mutate(from, to))
      assert.equal(r.code, 1, to)
      assert.match(r.out, /--duration-(fast|panel) FAIL, want/)
    }
  })

  it("fails when a duration token is missing", () => {
    const r = run(mutate("--duration-fast: 150ms;", ""))
    assert.equal(r.code, 1)
    assert.match(r.out, /--duration-fast FAIL, want 150ms, got undefined/)
  })
})
