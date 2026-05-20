import http from "node:http"

const SOCKET_SUFFIX = "devsy-local.sock"
const REQUEST_TIMEOUT = 3000

function getDefaultSocketPath(): string {
  if (process.platform === "win32") {
    return `\\\\.\\pipe\\${SOCKET_SUFFIX}`
  }
  return `/tmp/${SOCKET_SUFFIX}`
}

export class DaemonClient {
  private socketPath: string

  constructor(socketPath = getDefaultSocketPath()) {
    this.socketPath = socketPath
  }

  async health(): Promise<boolean> {
    try {
      await this.get("/health")
      return true
    } catch {
      return false
    }
  }

  async listWorkspaces<T>(): Promise<T> {
    return this.get<T>("/list")
  }

  async listProviders<T>(): Promise<T> {
    return this.get<T>("/provider/list")
  }

  async listMachines<T>(): Promise<T> {
    return this.get<T>("/machine/list")
  }

  async listContexts<T>(): Promise<T> {
    return this.get<T>("/context/list")
  }

  private get<T>(path: string): Promise<T> {
    return new Promise((resolve, reject) => {
      const req = http.get(
        { socketPath: this.socketPath, path, timeout: REQUEST_TIMEOUT },
        (res) => {
          const chunks: Buffer[] = []
          res.on("data", (chunk: Buffer) => chunks.push(chunk))
          res.on("end", () => {
            const body = Buffer.concat(chunks).toString()
            if (res.statusCode !== 200) {
              reject(new Error(`daemon ${path}: ${res.statusCode} ${body}`))
              return
            }
            try {
              resolve(JSON.parse(body) as T)
            } catch (e) {
              reject(e)
            }
          })
        },
      )
      req.on("error", reject)
      req.on("timeout", () => {
        req.destroy()
        reject(new Error(`daemon ${path}: timeout`))
      })
    })
  }
}
