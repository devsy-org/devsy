import { spawnSync } from "node:child_process"
import { createHash, randomUUID } from "node:crypto"
import {
  appendFileSync,
  mkdirSync,
  readFileSync,
  renameSync,
  writeFileSync,
} from "node:fs"
import { homedir } from "node:os"
import { dirname, isAbsolute, join, relative, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { stripVTControlCharacters } from "node:util"
import {
  type ElectronApplication,
  _electron as electron,
  expect,
  test,
} from "@playwright/test"

const required = (name: string): string => {
  const value = process.env[name]
  if (!value || !isAbsolute(value)) throw new Error(`${name} must be absolute`)
  return value
}
const root = required("REAL_CLI_ROOT")
const evidence = required("REAL_CLI_EVIDENCE")
const executable = required("REAL_CLI_ELECTRON")
const cli = required("DEVSY_CLI_PATH")
const config = required("DEVSY_CONFIG")
const desktop = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const home = join(root, "home")
const devsyHome = join(home, ".devsy")
const logs = join(root, "electron-logs")
const identity = JSON.parse(
  readFileSync(join(desktop, "package.json"), "utf8"),
) as {
  name: string
  version: string
}
const userData = join(root, "electron-user-data")
const sessionData = join(root, "electron-session-data")
const withinDevsyHome = relative(devsyHome, config)
if (
  !withinDevsyHome ||
  withinDevsyHome.startsWith("..") ||
  isAbsolute(withinDevsyHome)
)
  throw new Error("DEVSY_CONFIG must be inside HOME/.devsy")

const diagnostic = join(evidence, "real-cli-diagnostics.log")
let diagnosticBytes = 0
function log(label: string, value: string) {
  const text = `${label}: ${value}\n`.slice(
    0,
    Math.max(0, 256_000 - diagnosticBytes),
  )
  if (text) appendFileSync(diagnostic, text, { mode: 0o600 })
  diagnosticBytes += Buffer.byteLength(text)
}
function run(args: string[], succeeds = true) {
  const result = spawnSync(cli, args, {
    env: process.env,
    encoding: "utf8",
    timeout: 15_000,
    maxBuffer: 1_000_000,
  })
  log(
    `cli ${args.join(" ")} exit=${result.status}`,
    `${result.stdout}\n${result.stderr}\n${result.error ?? ""}`,
  )
  expect(result.error).toBeUndefined()
  if (succeeds) expect(result.status).toBe(0)
  else expect(result.status).not.toBe(0)
  return result
}

test("private context options reach the real Electron sheet and errors reach its toast", async () => {
  expect(process.platform).toBe("linux")
  expect(process.env.DEVSY_DISABLE_DAEMON).toBe("true")
  expect(process.env.DEVSY_LOG_FORMAT).toBe("text")
  expect(process.env.DEVSY_RESULT_FORMAT).toBe("text")
  expect(process.env.HOME).toBe(home)
  expect(homedir()).toBe(home)
  expect(process.env.DEVSY_HOME).toBe(devsyHome)
  expect(process.env.DEVSY_UI).toBeUndefined()
  expect(process.env.ELECTRON_RUN_AS_NODE).toBeUndefined()
  mkdirSync(userData, { recursive: true, mode: 0o700 })
  mkdirSync(sessionData, { recursive: true, mode: 0o700 })
  mkdirSync(logs, { recursive: true, mode: 0o700 })
  log(
    "candidate sha256",
    createHash("sha256").update(readFileSync(cli)).digest("hex"),
  )
  let app: ElectronApplication | undefined
  let originalConfig: Buffer | undefined
  let modified = false
  try {
    app = await electron.launch({
      executablePath: executable,
      args: ["--require", join(desktop, "e2e/real-cli-bootstrap.cjs"), desktop],
      env: Object.fromEntries(
        Object.entries(process.env).filter(
          (entry): entry is [string, string] => entry[1] !== undefined,
        ),
      ),
      timeout: 30_000,
    })
    app
      .process()
      .stdout?.on("data", (data) => log("electron stdout", String(data)))
    app
      .process()
      .stderr?.on("data", (data) => log("electron stderr", String(data)))
    const paths = await app.evaluate(({ app }) => ({
      platform: process.platform,
      packaged: app.isPackaged,
      home: process.getBuiltinModule("node:os").homedir(),
      electronHome: app.getPath("home"),
      envHome: process.env.HOME,
      devsyHome: process.env.DEVSY_HOME,
      config: process.env.DEVSY_CONFIG,
      name: app.getName(),
      version: app.getVersion(),
      appPath: app.getAppPath(),
      userData: app.getPath("userData"),
      sessionData: app.getPath("sessionData"),
      logs: app.getPath("logs"),
      cli: process.env.DEVSY_CLI_PATH,
    }))
    log("electron paths", JSON.stringify(paths))
    expect(paths).toEqual({
      platform: "linux",
      packaged: false,
      home,
      electronHome: home,
      envHome: home,
      devsyHome,
      config,
      name: identity.name,
      version: identity.version,
      appPath: desktop,
      userData,
      sessionData,
      logs,
      cli,
    })
    const page = await app.firstWindow({ timeout: 30_000 })
    page.on("console", (message) =>
      log(`renderer ${message.type()}`, message.text()),
    )
    page.on("pageerror", (error) =>
      log("renderer error", error.stack ?? error.message),
    )
    await expect(page.locator("body")).toBeVisible()
    const runningApp = app
    await expect
      .poll(() =>
        runningApp.evaluate(({ BrowserWindow }) =>
          BrowserWindow.getAllWindows().some(
            (window) => !window.isDestroyed() && window.isVisible(),
          ),
        ),
      )
      .toBe(true)
    run(["--version"])
    const name = `real-cli-${randomUUID()}`
    const value = "37"
    run(["context", "create", name, "-o", `AGENT_INJECT_TIMEOUT=${value}`])
    const list = JSON.parse(
      run(["context", "list", "--result-format=json"]).stdout,
    ) as Array<{ name: string; default?: boolean }>
    expect(list.find((entry) => entry.name === name)?.default).toBe(true)
    const plain = run(["context", "get", "--context", name]).stdout
    const visiblePlain = stripVTControlCharacters(plain)
    expect(visiblePlain).toContain("AGENT_INJECT_TIMEOUT")
    expect(visiblePlain).toMatch(/AGENT_INJECT_TIMEOUT[^\n]*\b37\b/)
    expect(() => JSON.parse(plain)).toThrow()
    const options = JSON.parse(
      run(["context", "get", "--context", name, "--result-format=json"]).stdout,
    ) as Record<string, { value?: string }>
    expect(options.AGENT_INJECT_TIMEOUT.value).toBe(value)
    await page.click('[data-sidebar="sidebar"] a[href="#/contexts"]')
    await expect(
      page.locator('[data-slot="sidebar-inset"] h1').first(),
    ).toContainText(/contexts/i)
    const card = page
      .locator('[data-slot="sidebar-inset"] main')
      .locator("button", {
        has: page.getByRole("heading", { name, exact: true }),
      })
    await expect(card).toBeVisible()
    await card.click()
    const sheet = page.getByRole("dialog")
    await expect(sheet).toBeVisible()
    await expect(sheet.getByPlaceholder("20")).toHaveValue(value)
    await page.screenshot({ path: join(evidence, "context-options.png") })
    await page.keyboard.press("Escape")
    await expect(sheet).toBeHidden()
    originalConfig = readFileSync(config)
    const temporary = `${config}.real-cli-invalid`
    writeFileSync(temporary, "contexts: [\n", { mode: 0o600, flag: "wx" })
    renameSync(temporary, config)
    modified = true
    const textFailure = run(
      ["context", "get", "--context", name, "--result-format=json"],
      false,
    )
    expect(textFailure.stderr).not.toContain('"kind":"error"')
    expect(textFailure.stderr).toContain("yaml:")
    const failure = run(
      [
        "context",
        "get",
        "--context",
        name,
        "--result-format=json",
        "--log-format=json",
      ],
      false,
    )
    expect(failure.stdout).toBe("")
    const envelope = failure.stderr
      .split(/\r?\n/)
      .map((line) => {
        try {
          return JSON.parse(line) as Record<string, unknown>
        } catch {
          return undefined
        }
      })
      .find((entry) => entry?.kind === "error" && entry.outcome === "error")
    expect(envelope).toBeDefined()
    expect(envelope?.code).toEqual(expect.any(String))
    expect(envelope?.message).toEqual(expect.any(String))
    expect(String(envelope?.code).length).toBeGreaterThan(0)
    expect(String(envelope?.message).length).toBeGreaterThan(0)
    expect(textFailure.stderr).toContain(String(envelope?.message))
    await card.click()
    const title = "Failed to load context options: "
    const message = String(envelope?.message)
    const cleanToast = `${title}${message}`
    const wrappedToast = `${title}Error invoking remote method 'context_options': Error: ${message}`
    const notifications = page.locator('section[aria-label^="Notifications"]')
    const toast = notifications
      .locator("[data-sonner-toast] [data-title]")
      .filter({ hasText: title })
    await expect(toast).toBeVisible()
    const actualToast = await toast.textContent()
    log("context options toast", actualToast ?? "")
    await page.screenshot({ path: join(evidence, "context-error.png") })
    expect([cleanToast, wrappedToast]).toContain(actualToast)
  } finally {
    try {
      if (modified && originalConfig)
        writeFileSync(config, originalConfig, { mode: 0o600 })
    } finally {
      await app?.close()
    }
  }
})
