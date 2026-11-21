import { execFileSync } from "node:child_process"
import { randomBytes } from "node:crypto"
import { readFileSync } from "node:fs"
import { platform } from "node:os"

let cached: string | undefined

function valid(value: string): string {
  const trimmed = value.trim()
  return trimmed.toLowerCase() === "unknown" ? "" : trimmed
}

export function machineIdSync(): string {
  if (cached !== undefined) return cached
  const os = platform()
  if (os === "linux") {
    for (const path of ["/var/lib/dbus/machine-id", "/etc/machine-id"]) {
      try {
        const id = readFileSync(path, "utf8").trim()
        if (id) {
          if (valid(id)) {
            cached = id
            return cached
          }
          break
        }
      } catch {}
    }
  } else if (os === "darwin" || os === "win32") {
    try {
      const output =
        os === "darwin"
          ? execFileSync("ioreg", ["-rd1", "-c", "IOPlatformExpertDevice"], {
              encoding: "utf8",
              timeout: 3000,
            })
          : execFileSync(
              "reg",
              [
                "query",
                "HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Cryptography",
                "/v",
                "MachineGuid",
                "/reg:64",
              ],
              { encoding: "utf8", timeout: 3000 },
            )
      const match =
        os === "darwin"
          ? output.match(/"IOPlatformUUID"\s*=\s*"([^"]+)"/)
          : output.match(/MachineGuid\s+REG_SZ\s+([^\r\n]+)/i)
      const id = valid(match?.[1] ?? "")
      if (id) {
        cached = id
        return cached
      }
    } catch {}
  }
  try {
    cached = randomBytes(32).toString("hex")
  } catch {
    cached = ""
  }
  return cached
}
