import type * as NodeChildProcess from "node:child_process"
import type * as NodeCrypto from "node:crypto"
import type * as NodeFs from "node:fs"
import { readFileSync } from "node:fs"
import type * as NodeOs from "node:os"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"
import { afterEach, describe, expect, it, vi } from "vitest"

const identityFixture = JSON.parse(
  readFileSync(
    resolve(
      dirname(fileURLToPath(import.meta.url)),
      "../../../../pkg/telemetry/testdata/identity.json",
    ),
    "utf8",
  ),
) as {
  version: number
  machine_vectors: Array<{
    platform: string
    native_inputs: NativeInputs
    normalized_machine_id: string
  }>
  failure_vectors: Array<{
    platform: string
    native_inputs: NativeInputs
    random_error: boolean
    fallback_seed: string | null
  }>
}

interface NativeInputs {
  dbus_machine_id: string | null
  etc_machine_id: string | null
  ioreg_output: string | null
  machine_guid: string | null
}

async function loadMachineId(
  os: string,
  files: Record<string, string | Error>,
  output = "",
  randomFails = false,
  seed = Buffer.alloc(32, 7),
) {
  vi.resetModules()
  const readFileSync = vi.fn((path: string) => {
    const value = files[path]
    if (value === undefined || value instanceof Error)
      throw value ?? new Error("missing")
    return value
  })
  const execFileSync = vi.fn(() => output)
  const randomBytes = vi.fn(() => {
    if (randomFails) throw new Error("random unavailable")
    return seed
  })
  vi.doMock("node:os", async (importOriginal) => {
    const original = await importOriginal<typeof NodeOs>()
    const platform = () => os
    return { ...original, platform, default: { ...original.default, platform } }
  })
  vi.doMock("node:fs", async (importOriginal) => {
    const original = await importOriginal<typeof NodeFs>()
    return {
      ...original,
      readFileSync,
      default: { ...original.default, readFileSync },
    }
  })
  vi.doMock("node:child_process", async (importOriginal) => {
    const original = await importOriginal<typeof NodeChildProcess>()
    return {
      ...original,
      execFileSync,
      default: { ...original.default, execFileSync },
    }
  })
  vi.doMock("node:crypto", async (importOriginal) => {
    const original = await importOriginal<typeof NodeCrypto>()
    return {
      ...original,
      randomBytes,
      default: { ...original.default, randomBytes },
    }
  })
  const { machineIdSync } = await import("../machine-id.js")
  return { machineIdSync, readFileSync, execFileSync, randomBytes }
}

async function loadFixtureReader(
  platform: string,
  inputs: NativeInputs,
  randomFails = false,
  seed = Buffer.alloc(32, 7),
) {
  const files: Record<string, string | Error> = {}
  if (inputs.dbus_machine_id !== null)
    files["/var/lib/dbus/machine-id"] = inputs.dbus_machine_id
  if (inputs.etc_machine_id !== null)
    files["/etc/machine-id"] = inputs.etc_machine_id
  const output =
    platform === "darwin"
      ? (inputs.ioreg_output ?? "")
      : inputs.machine_guid === null
        ? ""
        : `MachineGuid REG_SZ ${inputs.machine_guid}`
  return loadMachineId(
    platform === "windows" ? "win32" : platform,
    files,
    output,
    randomFails,
    seed,
  )
}

afterEach(() => {
  vi.doUnmock("node:os")
  vi.doUnmock("node:fs")
  vi.doUnmock("node:child_process")
  vi.doUnmock("node:crypto")
  vi.resetModules()
})

describe("native machine identity", () => {
  it("normalizes native sources from the shared identity fixture", async () => {
    expect(identityFixture.version).toBe(1)
    for (const vector of identityFixture.machine_vectors) {
      const reader = await loadFixtureReader(
        vector.platform,
        vector.native_inputs,
      )
      expect(reader.machineIdSync()).toBe(vector.normalized_machine_id)
    }
  })

  it("follows the shared native and randomness failure policy", async () => {
    for (const vector of identityFixture.failure_vectors) {
      const seed =
        vector.fallback_seed === null
          ? Buffer.alloc(32, 7)
          : Buffer.from(vector.fallback_seed, "hex")
      const reader = await loadFixtureReader(
        vector.platform,
        vector.native_inputs,
        vector.random_error,
        seed,
      )
      const identity = reader.machineIdSync()
      if (vector.random_error) {
        expect(identity).toBe("")
      } else {
        expect(identity).toBe(vector.fallback_seed)
      }
      expect(reader.machineIdSync()).toBe(identity)
      expect(reader.randomBytes).toHaveBeenCalledTimes(1)
    }
  })

  it("prefers the dbus ID and preserves case after trimming", async () => {
    const reader = await loadMachineId("linux", {
      "/var/lib/dbus/machine-id": "  AbC\n",
      "/etc/machine-id": "other",
    })
    expect(reader.machineIdSync()).toBe("AbC")
    expect(reader.machineIdSync()).toBe("AbC")
    expect(reader.readFileSync).toHaveBeenCalledTimes(1)
  })

  it("falls back to /etc for absent or empty dbus ID", async () => {
    for (const value of [new Error("missing"), "  "]) {
      const reader = await loadMachineId("linux", {
        "/var/lib/dbus/machine-id": value,
        "/etc/machine-id": " ETC-id\n",
      })
      expect(reader.machineIdSync()).toBe("ETC-id")
    }
  })

  it("rejects an unknown first native ID without selecting the second", async () => {
    const reader = await loadMachineId("linux", {
      "/var/lib/dbus/machine-id": "unknown",
      "/etc/machine-id": "other",
    })
    expect(reader.machineIdSync()).toBe(Buffer.alloc(32, 7).toString("hex"))
    expect(reader.readFileSync).toHaveBeenCalledTimes(1)
  })

  it("reads the platform UUID on macOS", async () => {
    const reader = await loadMachineId(
      "darwin",
      {},
      '  "IOPlatformUUID" = "AbC-123"\n',
    )
    expect(reader.machineIdSync()).toBe("AbC-123")
    expect(reader.execFileSync).toHaveBeenCalledWith(
      "ioreg",
      ["-rd1", "-c", "IOPlatformExpertDevice"],
      expect.any(Object),
    )
  })

  it("reads the 64-bit machine registry on Windows", async () => {
    const reader = await loadMachineId(
      "win32",
      {},
      "    MachineGuid    REG_SZ    AbC-123\r\n",
    )
    expect(reader.machineIdSync()).toBe("AbC-123")
    expect(reader.execFileSync).toHaveBeenCalledWith(
      "reg",
      expect.arrayContaining(["/reg:64", "MachineGuid"]),
      expect.any(Object),
    )
  })

  it("caches a random fallback and disables identity if secure randomness fails", async () => {
    const fallback = await loadMachineId("other", {})
    expect(fallback.machineIdSync()).toBe(Buffer.alloc(32, 7).toString("hex"))
    expect(fallback.machineIdSync()).toBe(fallback.machineIdSync())
    expect(fallback.randomBytes).toHaveBeenCalledTimes(1)
    const failed = await loadMachineId("other", {}, "", true)
    expect(failed.machineIdSync()).toBe("")
    expect(failed.machineIdSync()).toBe("")
    expect(failed.randomBytes).toHaveBeenCalledTimes(1)
  })
})
