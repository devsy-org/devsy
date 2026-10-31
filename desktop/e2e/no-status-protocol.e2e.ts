import { execFileSync } from "node:child_process"
import { existsSync, mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { expect, test } from "@playwright/test"
import { ProviderJobs } from "../src/main/provider-jobs.js"
import {
  normalizeOperationStatus,
  parseCliEnvelope,
} from "../src/shared/cli-error.js"

const DESKTOP_DIR = resolve(dirname(fileURLToPath(import.meta.url)), "..")
const REPO_DIR = resolve(DESKTOP_DIR, "..")

test("candidate CLI status reaches the Desktop timeline with --no-status", () => {
  test.setTimeout(120000)
  const binary = resolve(
    DESKTOP_DIR,
    "resources/bin",
    process.platform === "win32" ? "devsy.exe" : "devsy",
  )
  expect(existsSync(binary), `candidate CLI missing at ${binary}`).toBe(true)

  const home = mkdtempSync(join(tmpdir(), "devsy-desktop-protocol-"))
  const env = {
    ...process.env,
    DEVSY_HOME: home,
    DEVSY_UI: "true",
    DEVSY_DISABLE_TELEMETRY: "true",
  }
  const name = "e2e-no-status-protocol"
  const jobs = new ProviderJobs()
  jobs.start(name, "installing")

  try {
    const statusOutput = execFileSync(
      binary,
      [
        "--no-status",
        "--result-format=json",
        "--log-output=json",
        "provider",
        "add",
        resolve(REPO_DIR, "providers/docker/provider.yaml"),
        "--name",
        name,
        "--use=false",
      ],
      { encoding: "utf8", env },
    )
    const seenPhases: string[] = []
    for (const line of statusOutput.split(/\r?\n/)) {
      const envelope = parseCliEnvelope(line)
      if (envelope?.kind !== "status") continue
      seenPhases.push(`${envelope.phase}:${envelope.state}`)
      jobs.reportStatus(name, normalizeOperationStatus(envelope))
    }

    expect(seenPhases).toContain("installing_provider:started")
    expect(seenPhases).toContain("installing_provider:succeeded")
    expect(jobs.get(name)).toMatchObject({
      activity: "installing",
      phase: "installing_provider",
      state: "succeeded",
    })

    const listOutput = execFileSync(
      binary,
      ["--result-format=json", "provider", "list"],
      { encoding: "utf8", env },
    )
    const providers = JSON.parse(listOutput) as Record<string, unknown>
    expect(providers).toHaveProperty(name)
  } finally {
    rmSync(home, { recursive: true, force: true })
  }
})
