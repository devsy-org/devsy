// @vitest-environment node
import {
  chmodSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs"
import { tmpdir } from "node:os"
import { join, relative } from "node:path"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import {
  LinuxDesktopIntegration,
  type LinuxDesktopIntegrationOptions,
  parseExec,
  quoteExecArgument,
} from "../linux-desktop-integration.js"

const ICON_BYTES = Buffer.from("devsy-icon")
const OWNED_ENTRY = "sh.devsy.app.desktop"

let root: string

function write(path: string, content: string | Buffer): string {
  mkdirSync(join(path, ".."), { recursive: true })
  writeFileSync(path, content)
  return path
}

function appImage(name = "Devsy.AppImage"): string {
  return write(join(root, "apps", name), "appimage")
}

function userApplications(): string {
  return join(root, "data", "applications")
}

function userIcon(): string {
  return join(
    root,
    "data",
    "icons",
    "hicolor",
    "128x128",
    "apps",
    "sh.devsy.app.png",
  )
}

function integration(
  overrides: {
    env?: Record<string, string | undefined>
    platform?: NodeJS.Platform
    refresh?: LinuxDesktopIntegrationOptions["refreshDesktopDatabase"]
  } = {},
): LinuxDesktopIntegration {
  return new LinuxDesktopIntegration({
    platform: overrides.platform ?? "linux",
    env: {
      XDG_DATA_HOME: join(root, "data"),
      XDG_DATA_DIRS: `${join(root, "sys-a")}:${join(root, "sys-b")}`,
      ...overrides.env,
    },
    homeDir: join(root, "home"),
    iconSource: write(join(root, "icon-source.png"), ICON_BYTES),
    refreshDesktopDatabase: overrides.refresh ?? (async () => {}),
  })
}

beforeEach(() => {
  root = mkdtempSync(join(tmpdir(), "devsy-integration-"))
})

afterEach(() => {
  vi.restoreAllMocks()
  chmodSync(root, 0o700)
  rmSync(root, { recursive: true, force: true })
})

describe("Exec quoting", () => {
  it.each([
    "/home/u/Devsy.AppImage",
    "/home/u/My Apps/Devsy.AppImage",
    '/home/u/say "hi"/Devsy.AppImage',
    "/home/u/it's/Devsy.AppImage",
    "/home/u/back\\slash/Devsy.AppImage",
    "/home/u/$HOME/`id`/Devsy.AppImage",
    "/home/u/100%/Devsy.AppImage",
    "/home/u/%U/Devsy.AppImage",
    "/home/u/caf\u00e9 (1)/Devsy.AppImage",
  ])("round-trips %s through the desktop file", (path) => {
    expect(parseExec(`${quoteExecArgument(path)} %U`)).toEqual([path, "%U"])
  })

  it("keeps a literal percent distinct from a field code", () => {
    expect(quoteExecArgument("/a/100%/x")).toBe("/a/100%%/x")
  })

  it("quotes only arguments that contain reserved characters", () => {
    expect(quoteExecArgument("/a/b")).toBe("/a/b")
    expect(quoteExecArgument("/a b/c")).toBe('"/a b/c"')
  })

  it.each([
    ["/a/$cash", String.raw`"/a/\\$cash"`],
    ['/a/a"b', String.raw`"/a/a\\"b"`],
    [String.raw`/a/a\b`, String.raw`"/a/a\\\\b"`],
  ])("applies both Desktop Entry escaping layers to %s", (path, expected) => {
    expect(quoteExecArgument(path)).toBe(expected)
  })
})

describe("status", () => {
  it("is unsupported outside an AppImage on Linux", () => {
    expect(
      integration({ env: { APPIMAGE: undefined } }).status().supported,
    ).toBe(false)
    expect(
      integration({
        platform: "darwin",
        env: { APPIMAGE: appImage() },
      }).status().supported,
    ).toBe(false)
  })

  it.each([
    ["a relative path", () => "Devsy.AppImage", "absolute"],
    ["a path containing '='", () => appImage("a=b.AppImage"), "'='"],
    ["a missing file", () => join(root, "apps", "gone.AppImage"), "not a file"],
    ["a directory", () => join(root, "apps"), "not a file"],
    ["a path with a newline", () => appImage("a\nb.AppImage"), "absolute"],
  ])(
    "reports %s as a problem and refuses to install",
    async (_name, path, message) => {
      const target = integration({ env: { APPIMAGE: path() } })
      expect(target.status().problem).toContain(message)
      const result = await target.install()
      expect(result.error).toContain(message)
      expect(existsSync(userApplications())).toBe(false)
    },
  )

  it("ignores relative XDG_DATA_HOME and XDG_DATA_DIRS entries", () => {
    const relativeDir = relative(process.cwd(), join(root, "relative-share"))
    write(
      join(root, "relative-share", "applications", OWNED_ENTRY),
      "[Desktop Entry]\nName=x\n",
    )
    const target = integration({
      env: {
        APPIMAGE: appImage(),
        XDG_DATA_HOME: relative(process.cwd(), join(root, "relative-home")),
        XDG_DATA_DIRS: relativeDir,
      },
    })
    expect(target.status().conflict).toBeUndefined()
  })
})

describe("install", () => {
  it("writes a launcher and icon that identify and reopen this AppImage", async () => {
    const path = appImage("My Apps Devsy.AppImage")
    const refresh = vi.fn(async () => {})
    const result = await integration({
      env: { APPIMAGE: path },
      refresh,
    }).install()

    expect(result.error).toBeUndefined()
    expect(result.status).toMatchObject({ installed: true, needsUpdate: false })
    const entry = readFileSync(join(userApplications(), OWNED_ENTRY), "utf-8")
    const exec = entry.split("\n").find((line) => line.startsWith("Exec="))
    expect(parseExec(exec?.slice("Exec=".length) ?? "")).toEqual([path, "%U"])
    expect(entry).toContain("Icon=sh.devsy.app\n")
    expect(entry).toContain("StartupWMClass=sh.devsy.app\n")
    expect(entry).toContain("MimeType=x-scheme-handler/devsy;\n")
    expect(entry).toContain("Terminal=false\n")
    expect(readFileSync(userIcon())).toEqual(ICON_BYTES)
    expect(refresh).toHaveBeenCalledWith(userApplications())
  })

  it("never changes the default URL handler", async () => {
    await integration({ env: { APPIMAGE: appImage() } }).install()
    expect(
      existsSync(join(root, "data", "applications", "mimeapps.list")),
    ).toBe(false)
    expect(existsSync(join(root, "home", ".config", "mimeapps.list"))).toBe(
      false,
    )
  })

  it.each([
    [
      "a system package",
      () => join(root, "sys-b", "applications", OWNED_ENTRY),
    ],
    [
      "a user Flatpak export",
      () =>
        join(
          root,
          "home",
          ".local",
          "share",
          "flatpak",
          "exports",
          "share",
          "applications",
          OWNED_ENTRY,
        ),
    ],
  ])(
    "refuses when %s already provides the launcher",
    async (_name, entryPath) => {
      const existing = write(entryPath(), "[Desktop Entry]\nName=Devsy\n")
      const result = await integration({
        env: { APPIMAGE: appImage() },
      }).install()

      expect(result.error).toContain(existing)
      expect(result.status.conflict).toContain(existing)
      expect(existsSync(join(userApplications(), OWNED_ENTRY))).toBe(false)
      expect(existsSync(userIcon())).toBe(false)
    },
  )

  it("never overwrites a launcher the user manages", async () => {
    const mine = write(
      join(userApplications(), OWNED_ENTRY),
      "[Desktop Entry]\nName=Mine\n",
    )
    const result = await integration({
      env: { APPIMAGE: appImage() },
    }).install()

    expect(result.error).toContain(mine)
    expect(readFileSync(mine, "utf-8")).toBe("[Desktop Entry]\nName=Mine\n")
    expect(result.status.installed).toBe(false)
  })

  it("detects this AppImage integrated under another file name", async () => {
    const path = appImage("My Devsy.AppImage")
    const foreign = write(
      join(root, "sys-a", "applications", "appimagekit_abc-Devsy.desktop"),
      `[Desktop Entry]\nName=Devsy\nExec="${path}" %U\n`,
    )
    const result = await integration({ env: { APPIMAGE: path } }).install()

    expect(result.error).toContain(foreign)
    expect(existsSync(join(userApplications(), OWNED_ENTRY))).toBe(false)
  })

  it("ignores launchers for a different AppImage", async () => {
    write(
      join(root, "sys-a", "applications", "other.desktop"),
      `[Desktop Entry]\nName=Other\nExec=${appImage("Other.AppImage")} %U\n`,
    )
    const result = await integration({
      env: { APPIMAGE: appImage() },
    }).install()
    expect(result.error).toBeUndefined()
    expect(result.status.installed).toBe(true)
  })

  it("adds a launcher without claiming an existing foreign icon", async () => {
    const foreignIcon = Buffer.from("someone else's icon")
    write(userIcon(), foreignIcon)
    const target = integration({ env: { APPIMAGE: appImage() } })
    const result = await target.install()
    expect(result.error).toBeUndefined()
    expect(result.status.installed).toBe(true)
    expect(readFileSync(userIcon())).toEqual(foreignIcon)
    await target.remove()
    expect(existsSync(join(userApplications(), OWNED_ENTRY))).toBe(false)
    expect(readFileSync(userIcon())).toEqual(foreignIcon)
  })

  it.skipIf(process.getuid?.() === 0)(
    "reports an unwritable data directory and stays usable",
    async () => {
      mkdirSync(join(root, "data"), { recursive: true })
      chmodSync(join(root, "data"), 0o500)
      const target = integration({ env: { APPIMAGE: appImage() } })
      const result = await target.install()

      expect(result.error).toContain("Could not write")
      expect(result.status).toMatchObject({ supported: true, installed: false })
      chmodSync(join(root, "data"), 0o700)
      expect((await target.install()).status.installed).toBe(true)
    },
  )
})

describe("moved AppImage", () => {
  it("follows the AppImage to its new path without creating anything new", async () => {
    const before = appImage("Old.AppImage")
    await integration({ env: { APPIMAGE: before } }).install()

    const after = appImage("New.AppImage")
    const moved = integration({ env: { APPIMAGE: after } })
    expect(moved.status()).toMatchObject({ installed: true, needsUpdate: true })

    const result = await moved.refreshOwned()
    expect(result.status).toMatchObject({ installed: true, needsUpdate: false })
    const entry = readFileSync(join(userApplications(), OWNED_ENTRY), "utf-8")
    expect(entry).toContain(`Exec=${after} %U`)
    expect(entry).not.toContain("Old.AppImage")
    expect((await moved.remove()).error).toBeUndefined()
    expect(existsSync(join(userApplications(), OWNED_ENTRY))).toBe(false)
    expect(existsSync(userIcon())).toBe(false)
  })

  it("leaves a declined launcher absent", async () => {
    const result = await integration({
      env: { APPIMAGE: appImage() },
    }).refreshOwned()
    expect(result.status.installed).toBe(false)
    expect(existsSync(userApplications())).toBe(false)
  })

  it("restores an icon that was deleted", async () => {
    const target = integration({ env: { APPIMAGE: appImage() } })
    await target.install()
    rmSync(userIcon())
    expect(target.status().needsUpdate).toBe(true)

    await target.refreshOwned()
    expect(readFileSync(userIcon())).toEqual(ICON_BYTES)
  })

  it("does not rewrite a launcher while another one shadows it", async () => {
    const target = integration({ env: { APPIMAGE: appImage("Old.AppImage") } })
    await target.install()
    write(
      join(root, "sys-a", "applications", OWNED_ENTRY),
      "[Desktop Entry]\nName=Devsy\n",
    )
    const moved = integration({ env: { APPIMAGE: appImage("New.AppImage") } })

    const result = await moved.refreshOwned()
    expect(result.error).toBeUndefined()
    expect(
      readFileSync(join(userApplications(), OWNED_ENTRY), "utf-8"),
    ).toContain("Old.AppImage")
  })
})

describe("remove", () => {
  it("deletes the launcher and icon it wrote", async () => {
    const target = integration({ env: { APPIMAGE: appImage() } })
    await target.install()
    const result = await target.remove()

    expect(result.error).toBeUndefined()
    expect(result.status.installed).toBe(false)
    expect(existsSync(join(userApplications(), OWNED_ENTRY))).toBe(false)
    expect(existsSync(userIcon())).toBe(false)
  })

  it("refuses to delete a launcher it does not own", async () => {
    const mine = write(
      join(userApplications(), OWNED_ENTRY),
      "[Desktop Entry]\nName=Mine\n",
    )
    const result = await integration({ env: { APPIMAGE: appImage() } }).remove()

    expect(result.error).toBeDefined()
    expect(existsSync(mine)).toBe(true)
  })

  it("keeps an icon that another Devsy launcher uses", async () => {
    const target = integration({ env: { APPIMAGE: appImage() } })
    await target.install()
    write(
      join(root, "sys-b", "applications", "devsy-desktop.desktop"),
      "[Desktop Entry]\nName=Devsy\nIcon=sh.devsy.app\n",
    )
    await target.remove()

    expect(existsSync(join(userApplications(), OWNED_ENTRY))).toBe(false)
    expect(readFileSync(userIcon())).toEqual(ICON_BYTES)
  })

  it("keeps an icon modified after integration", async () => {
    const target = integration({ env: { APPIMAGE: appImage() } })
    await target.install()
    write(userIcon(), "someone else's icon")
    await target.remove()
    expect(readFileSync(userIcon(), "utf-8")).toBe("someone else's icon")
  })
})

describe("ownership boundaries", () => {
  it("does not run in Flatpak even with an inherited APPIMAGE", async () => {
    const target = integration({
      env: { APPIMAGE: appImage(), FLATPAK_ID: "sh.devsy.app" },
    })
    expect(target.status().supported).toBe(false)
    expect((await target.install()).error).toBeDefined()
    expect(existsSync(userApplications())).toBe(false)
  })

  it("does not take over another package's deep-link handler", async () => {
    const entry = write(
      join(root, "sys-a", "applications", "devsy-desktop.desktop"),
      "[Desktop Entry]\nName=Devsy\nMimeType=x-scheme-handler/devsy;\nExec=/opt/Devsy/devsy-desktop %U\n",
    )
    const result = await integration({
      env: { APPIMAGE: appImage() },
    }).install()
    expect(result.status.conflict).toContain(entry)
    expect(existsSync(userApplications())).toBe(false)
  })

  it("never deletes an arbitrary path recorded as the owned icon", async () => {
    const target = integration({ env: { APPIMAGE: appImage() } })
    await target.install()
    const victim = write(join(root, "keep.png"), "keep")
    const path = join(userApplications(), OWNED_ENTRY)
    writeFileSync(
      path,
      readFileSync(path, "utf8").replace(
        /^X-Devsy-AppImage-Icon=.*$/m,
        `X-Devsy-AppImage-Icon=${victim}`,
      ),
    )
    await target.remove()
    expect(readFileSync(victim, "utf8")).toBe("keep")
  })

  it("fails closed on an unreadable launcher", async () => {
    const directory = join(userApplications(), OWNED_ENTRY)
    mkdirSync(directory, { recursive: true })
    const result = await integration({
      env: { APPIMAGE: appImage() },
    }).install()
    expect(result.error).toContain("Could not inspect launcher")
    expect(existsSync(userIcon())).toBe(false)
  })

  it("does not remove an owned-looking symlink", async () => {
    const target = integration({ env: { APPIMAGE: appImage() } })
    await target.install()
    const userEntry = join(userApplications(), OWNED_ENTRY)
    const original = join(root, "keep.desktop")
    write(original, readFileSync(userEntry))
    rmSync(userEntry)
    symlinkSync(original, userEntry)
    const result = await target.remove()
    expect(result.error).toBeDefined()
    expect(readFileSync(original, "utf8")).toContain("Name=Devsy")
    expect(existsSync(userEntry)).toBe(true)
  })

  it("installs under the home fallback for a relative XDG data home", async () => {
    const target = integration({
      env: {
        APPIMAGE: appImage("trailing space "),
        XDG_DATA_HOME: "relative-data",
      },
    })
    const result = await target.install()
    expect(result.error).toBeUndefined()
    expect(result.status.needsUpdate).toBe(false)
    expect(
      existsSync(join(root, "home", ".local/share/applications", OWNED_ENTRY)),
    ).toBe(true)
    await target.remove()
  })

  it("detects a third-party integration that uses a symlink to the AppImage", async () => {
    const path = appImage()
    const alias = join(root, "alias.AppImage")
    symlinkSync(path, alias)
    const entry = write(
      join(root, "sys-a/applications/integrator.desktop"),
      `[Desktop Entry]\nExec=${quoteExecArgument(alias)} %U\n`,
    )
    const result = await integration({ env: { APPIMAGE: path } }).install()
    expect(result.status.conflict).toContain(entry)
    expect(existsSync(userIcon())).toBe(false)
  })
})

describe("launcher and icon ownership", () => {
  it.each(["Exec", "Name", "custom field"] as const)(
    "preserves a user-edited %s launcher through move, refresh and removal",
    async (change) => {
      const original = integration({
        env: { APPIMAGE: appImage("Old.AppImage") },
      })
      expect((await original.install()).error).toBeUndefined()
      const path = join(userApplications(), OWNED_ENTRY)
      const generated = readFileSync(path, "utf8")
      const edited =
        change === "Exec"
          ? generated.replace(/^(Exec=.*)$/m, "$1 --custom-argument")
          : change === "Name"
            ? generated.replace(/^Name=.*$/m, "Name=My development launcher")
            : `${generated}X-Local-Preference=keep-me\n`
      writeFileSync(path, edited)
      const expectedBytes = readFileSync(path)
      const moved = integration({
        env: { APPIMAGE: appImage("Moved.AppImage") },
      })

      const refreshed = await moved.refreshOwned()
      expect(refreshed.status.conflict).toBeDefined()
      expect(refreshed.status.installed).toBe(false)
      expect(readFileSync(path)).toEqual(expectedBytes)
      expect((await moved.remove()).error).toBeDefined()
      expect(readFileSync(path)).toEqual(expectedBytes)
    },
  )

  it.each(["install", "remove"] as const)(
    "rechecks an edit made after the status check before %s",
    async (operation) => {
      const target = integration({ env: { APPIMAGE: appImage() } })
      expect((await target.install()).error).toBeUndefined()
      const path = join(userApplications(), OWNED_ENTRY)
      const edited = Buffer.from(
        `${readFileSync(path, "utf8")}# keep this edit\n`,
      )
      const status = target.status.bind(target)
      vi.spyOn(target, "status").mockImplementationOnce(() => {
        const checked = status()
        writeFileSync(path, edited)
        return checked
      })

      const result =
        operation === "install" ? await target.install() : await target.remove()
      expect(result.error).toBeDefined()
      expect(readFileSync(path)).toEqual(edited)
    },
  )

  it("rechecks launcher bytes immediately before replacing the prepared file", async () => {
    const path = join(userApplications(), OWNED_ENTRY)
    const source = write(join(root, "source-icon.png"), ICON_BYTES)
    let editBeforeReplacement = false
    let edited = ""
    const target = new LinuxDesktopIntegration({
      platform: "linux",
      env: {
        APPIMAGE: appImage(),
        XDG_DATA_HOME: join(root, "data"),
        XDG_DATA_DIRS: join(root, "system"),
      },
      homeDir: join(root, "home"),
      get iconSource() {
        if (editBeforeReplacement) writeFileSync(path, edited)
        return source
      },
      refreshDesktopDatabase: async () => {},
    })
    expect((await target.install()).error).toBeUndefined()
    edited = `${readFileSync(path, "utf8")}X-Local-Preference=concurrent-edit\n`
    rmSync(userIcon())
    editBeforeReplacement = true

    const result = await target.install()
    expect(result.error).toBeDefined()
    expect(readFileSync(path, "utf8")).toBe(edited)
  })

  it("repairs a moved AppImage while preserving a customized icon", async () => {
    const original = integration({
      env: { APPIMAGE: appImage("Old.AppImage") },
    })
    expect((await original.install()).error).toBeUndefined()
    const customized = Buffer.from("custom icon bytes")
    write(userIcon(), customized)
    const movedPath = appImage("Moved.AppImage")
    const moved = integration({ env: { APPIMAGE: movedPath } })

    const result = await moved.refreshOwned()
    expect(result.error).toBeUndefined()
    expect(result.status).toMatchObject({ installed: true, needsUpdate: false })
    const entry = readFileSync(join(userApplications(), OWNED_ENTRY), "utf8")
    const exec = entry.split("\n").find((line) => line.startsWith("Exec="))
    expect(parseExec(exec?.slice(5) ?? "")).toEqual([movedPath, "%U"])
    expect(readFileSync(userIcon())).toEqual(customized)
    expect((await moved.remove()).error).toBeUndefined()
    expect(readFileSync(userIcon())).toEqual(customized)
  })

  it("re-adds after preserving a shared icon without adopting its ownership", async () => {
    const target = integration({ env: { APPIMAGE: appImage() } })
    expect((await target.install()).error).toBeUndefined()
    const otherEntry = write(
      join(root, "sys-a", "applications", "other-launcher.desktop"),
      "[Desktop Entry]\nName=Other launcher\nIcon=sh.devsy.app\n",
    )
    const sharedBytes = readFileSync(userIcon())
    expect((await target.remove()).error).toBeUndefined()
    expect(readFileSync(userIcon())).toEqual(sharedBytes)

    const result = await target.install()
    expect(result.error).toBeUndefined()
    expect(result.status.installed).toBe(true)
    expect(readFileSync(userIcon())).toEqual(sharedBytes)
    rmSync(otherEntry)
    expect((await target.remove()).error).toBeUndefined()
    expect(readFileSync(userIcon())).toEqual(sharedBytes)
  })
})

describe("shared icon path repair", () => {
  it("never overwrites or later adopts an icon shared during a path update", async () => {
    const original = integration({
      env: { APPIMAGE: appImage("Old.AppImage") },
    })
    expect((await original.install()).error).toBeUndefined()
    const otherEntry = write(
      join(root, "sys-a", "applications", "other-launcher.desktop"),
      "[Desktop Entry]\nName=Other launcher\nIcon=sh.devsy.app\n",
    )
    const sharedBytes = readFileSync(userIcon())
    const moved = integration({ env: { APPIMAGE: appImage("Moved.AppImage") } })
    write(join(root, "icon-source.png"), "new bundled artwork")

    const result = await moved.refreshOwned()
    expect(result.error).toBeUndefined()
    expect(result.status).toMatchObject({ installed: true, needsUpdate: false })
    expect(readFileSync(userIcon())).toEqual(sharedBytes)
    rmSync(otherEntry)
    expect((await moved.remove()).error).toBeUndefined()
    expect(readFileSync(userIcon())).toEqual(sharedBytes)
  })
})
