import { randomUUID } from "node:crypto"

/** Main-process-only unlock coordination. Credentials are never returned to the renderer. */
interface PendingSecretSession {
  id: string
  promise: Promise<string | undefined>
  resolve: (value: string | undefined) => void
  timer: ReturnType<typeof setTimeout>
  cleanup?: () => void
  notify: (id: string) => boolean
}

export class SecretSession {
  private pending?: PendingSecretSession

  constructor(private readonly timeoutMs = 5 * 60 * 1000) {}

  request(
    notify: (id: string) => boolean,
    registerCancellation?: (cancel: () => void) => () => void,
  ): Promise<string | undefined> {
    if (this.pending) {
      const pending = this.pending
      this.notifyPending()
      return pending.promise
    }

    let resolve!: (value: string | undefined) => void
    const promise = new Promise<string | undefined>((done) => {
      resolve = done
    })
    const id = randomUUID()
    const timer = setTimeout(() => this.settle(id, undefined), this.timeoutMs)
    const pending: PendingSecretSession = {
      id,
      promise,
      resolve,
      timer,
      notify,
    }
    this.pending = pending

    if (registerCancellation) {
      try {
        const cleanup = registerCancellation(() => this.settle(id, undefined))
        if (this.pending === pending) pending.cleanup = cleanup
        else cleanup()
      } catch {
        this.settle(id, undefined)
        return promise
      }
    }
    this.notifyPending()
    return promise
  }

  notifyPending(): void {
    const pending = this.pending
    if (!pending) return
    try {
      if (!pending.notify(pending.id)) this.settle(pending.id, undefined)
    } catch {
      this.settle(pending.id, undefined)
    }
  }

  capturePendingId(): string | undefined {
    return this.pending?.id
  }

  isPending(id: string | undefined): id is string {
    return id !== undefined && this.pending?.id === id
  }

  submit(id: string | undefined, value: string | undefined): boolean {
    if (!this.isPending(id)) return false
    this.settle(id, value)
    return true
  }

  cancelCurrent(): void {
    if (this.pending) this.settle(this.pending.id, undefined)
  }

  private settle(id: string, value: string | undefined): void {
    const pending = this.pending
    if (!pending || pending.id !== id) return
    this.pending = undefined
    clearTimeout(pending.timer)
    try {
      pending.cleanup?.()
    } catch {
      // Cleanup failures must not strand callers waiting for this request.
    } finally {
      pending.resolve(value)
    }
  }
}
