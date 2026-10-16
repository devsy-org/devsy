// @vitest-environment node
import { describe, expect, it } from "vitest"
import { desktopExecutablePath } from "../executable-path.js"

describe("desktopExecutablePath", () => {
  it("puts official user-local installations before inherited version-manager shims", () => {
    const path = desktopExecutablePath(
      "/Users/tester/.local/share/mise/shims:/usr/bin:/bin",
      "darwin",
      "/Users/tester",
    ).split(":")
    expect(path).toContain("/Users/tester/.local/bin")
    expect(path.indexOf("/Users/tester/.local/bin")).toBeLessThan(
      path.indexOf("/Users/tester/.local/share/mise/shims"),
    )
    expect(path.slice(0, 3)).toEqual([
      "/usr/local/bin",
      "/opt/homebrew/bin",
      "/opt/homebrew/sbin",
    ])
  })

  it("keeps inherited directories in order without duplicate or empty entries", () => {
    const path = desktopExecutablePath(
      ":/usr/bin:/opt/homebrew/bin:/Users/tester/.local/bin:/usr/bin:/bin:",
      "darwin",
      "/Users/tester",
    ).split(":")
    expect(
      path.filter((entry) => entry === "/Users/tester/.local/bin"),
    ).toHaveLength(1)
    expect(path.filter((entry) => entry === "/opt/homebrew/bin")).toHaveLength(
      1,
    )
    expect(path.filter((entry) => entry === "/usr/bin")).toHaveLength(1)
    expect(path).not.toContain("")
    expect(path.indexOf("/usr/bin")).toBeLessThan(path.indexOf("/bin"))
  })

  it.each(["linux", "win32"] as const)(
    "preserves the inherited %s search path",
    (platform) => {
      const path =
        platform === "win32" ? "C:\\Tools;C:\\Windows" : "/usr/bin:/bin"
      expect(desktopExecutablePath(path, platform, "/home/tester")).toBe(path)
    },
  )
})
