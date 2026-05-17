import { execSync } from "node:child_process"
import { createHash } from "node:crypto"
import { platform } from "node:os"

export function machineIdSync(): string {
  try {
    const os = platform()
    let raw: string

    if (os === "linux") {
      raw = execSync("cat /etc/machine-id || cat /var/lib/dbus/machine-id", {
        encoding: "utf-8",
        timeout: 3000,
      }).trim()
    } else if (os === "darwin") {
      const output = execSync(
        "ioreg -rd1 -c IOPlatformExpertDevice | awk '/IOPlatformUUID/'",
        { encoding: "utf-8", timeout: 3000 },
      )
      const match = output.match(/"([^"]+)"$/)
      raw = match?.[1] ?? "unknown"
    } else if (os === "win32") {
      const output = execSync(
        "reg query HKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Cryptography /v MachineGuid",
        { encoding: "utf-8", timeout: 3000 },
      )
      const match = output.match(/REG_SZ\s+(.+)/)
      raw = match?.[1]?.trim() ?? "unknown"
    } else {
      raw = "unknown"
    }

    return createHash("sha256").update(raw).digest("hex")
  } catch {
    return "unknown"
  }
}
