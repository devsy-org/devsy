import { spawn } from "node:child_process"
import { DaemonClient } from "./daemon-client.js"

const HEALTH_INTERVAL = 30_000
const SPAWN_RETRY_DELAY = 2000
const MAX_SPAWN_RETRIES = 3

export class DaemonManager {
  private client: DaemonClient
  private healthTimer: ReturnType<typeof setInterval> | null = null
  private running = false
  private binaryPath: string
  private prefixArgs: string[]

  constructor(binaryPath: string) {
    this.client = new DaemonClient()
    if (/\.[cm]?js$/.test(binaryPath)) {
      this.binaryPath = "node"
      this.prefixArgs = [binaryPath]
    } else {
      this.binaryPath = binaryPath
      this.prefixArgs = []
    }
  }

  get daemonClient(): DaemonClient {
    return this.client
  }

  async start(): Promise<void> {
    this.running = true
    await this.ensureRunning()
    this.healthTimer = setInterval(() => this.ensureRunning(), HEALTH_INTERVAL)
  }

  stop(): void {
    this.running = false
    if (this.healthTimer) {
      clearInterval(this.healthTimer)
      this.healthTimer = null
    }
  }

  private async ensureRunning(): Promise<void> {
    if (!this.running) return

    const alive = await this.client.health()
    if (alive) return

    for (let i = 0; i < MAX_SPAWN_RETRIES; i++) {
      this.spawnDaemon()
      await sleep(SPAWN_RETRY_DELAY)
      if (await this.client.health()) return
    }
  }

  private spawnDaemon(): void {
    const child = spawn(
      this.binaryPath,
      [...this.prefixArgs, "daemon-local"],
      { detached: true, stdio: "ignore" },
    )
    child.unref()
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms))
}
