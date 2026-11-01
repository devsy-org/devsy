// @vitest-environment node
import { join } from "node:path"
import { describe, expect, it } from "vitest"
import {
  linuxWindowIcon,
  PACKAGED_ICON_FILE,
} from "../linux-desktop-identity.js"

const context = {
  isPackaged: true,
  resourcesPath: "/opt/Devsy/resources",
  appPath: "/work/desktop",
}

describe("linuxWindowIcon", () => {
  it("uses the icon shipped in resources when packaged", () => {
    expect(linuxWindowIcon({ ...context, platform: "linux" })).toBe(
      join("/opt/Devsy/resources", PACKAGED_ICON_FILE),
    )
  })

  it("uses the source tree icon in development", () => {
    expect(
      linuxWindowIcon({ ...context, platform: "linux", isPackaged: false }),
    ).toBe(join("/work/desktop", "resources", "icon.png"))
  })

  it.each(["darwin", "win32"] as const)("sets no icon on %s", (platform) => {
    expect(linuxWindowIcon({ ...context, platform })).toBeUndefined()
  })
})
