/** Main-process-only unlock coordination. Credentials are never returned to the renderer. */
export class SecretSession {
  private pending?: {
    promise: Promise<string | undefined>
    resolve: (value: string | undefined) => void
  }

  request(notify: () => boolean): Promise<string | undefined> {
    if (this.pending) return this.pending.promise
    let resolve!: (value: string | undefined) => void
    const promise = new Promise<string | undefined>((done) => {
      resolve = done
    })
    this.pending = { promise, resolve }
    if (!notify()) this.submit(undefined)
    return promise
  }

  submit(value: string | undefined): void {
    const pending = this.pending
    this.pending = undefined
    pending?.resolve(value)
  }
}
