import { readFileSync } from "node:fs"
import { resolve } from "node:path"
import { describe, expect, it } from "vitest"

const root = resolve(__dirname, "../../..")
const read = (path: string) => readFileSync(resolve(root, path), "utf8")

describe("third-party notices", () => {
  const notices = read("resources/THIRD_PARTY_NOTICES.txt")
  const font = "node_modules/@fontsource-variable/inter"

  it("carries the installed Inter licence text", () => {
    expect(notices).toContain(read(`${font}/LICENSE`).trim())
  })

  it("names the installed Inter version", () => {
    const { version } = JSON.parse(read(`${font}/package.json`))
    expect(notices).toContain(`@fontsource-variable/inter ${version}`)
  })

  it("is shipped as a packaged resource", () => {
    const builder = read("electron-builder.yml")
    expect(builder).toMatch(
      /from: resources\/THIRD_PARTY_NOTICES\.txt\n\s+to: THIRD_PARTY_NOTICES\.txt/,
    )
  })

  it("is kept out of app.asar", () => {
    const builder = read("electron-builder.yml")
    expect(builder).toMatch(/^ {2}- "!resources\/THIRD_PARTY_NOTICES\.txt"$/m)
  })

  it("does not ship the Inter package at runtime", () => {
    const pkg = JSON.parse(read("package.json"))
    expect(pkg.dependencies).not.toHaveProperty("@fontsource-variable/inter")
    expect(pkg.devDependencies).toHaveProperty("@fontsource-variable/inter")
  })
})
