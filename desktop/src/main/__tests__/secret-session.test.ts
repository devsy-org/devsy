// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest"
import { SecretSession } from "../secret-session.js"

describe("SecretSession", () => {
  afterEach(() => vi.useRealTimers())

  it("coordinates concurrent unlock requests and re-notifies without returning credentials", async () => {
    const session = new SecretSession()
    const notify = vi.fn(() => true)
    const first = session.request(notify)
    const second = session.request(() => true)
    expect(first).toBe(second)
    expect(notify).toHaveBeenCalledTimes(2)
    session.submit("private credential")
    await expect(first).resolves.toBe("private credential")
  })

  it("times out and permits a fresh unlock request", async () => {
    vi.useFakeTimers()
    const session = new SecretSession(1_000)
    const first = session.request(() => true)
    await vi.advanceTimersByTimeAsync(1_000)
    await expect(first).resolves.toBeUndefined()
    expect(vi.getTimerCount()).toBe(0)

    const second = session.request(() => true)
    session.submit("next credential")
    await expect(second).resolves.toBe("next credential")
  })

  it("does not let a stale cancellation affect a newer request", async () => {
    const session = new SecretSession()
    let cancelFirst!: () => void
    const first = session.request(
      () => true,
      (cancel) => {
        cancelFirst = cancel
        return () => undefined
      },
    )
    session.submit(undefined)
    await expect(first).resolves.toBeUndefined()

    const second = session.request(() => true)
    cancelFirst()
    session.submit("new credential")
    await expect(second).resolves.toBe("new credential")
  })

  it("returns the joined promise even when re-notifying settles the session", async () => {
    const session = new SecretSession()
    let available = true
    const pending = session.request(() => available)
    available = false
    const joined = session.request(() => true)
    expect(joined).toBe(pending)
    await expect(joined).resolves.toBeUndefined()
  })

  it("resolves when notification throws and cleans up registered listeners", async () => {
    const cleanup = vi.fn()
    const session = new SecretSession()
    await expect(
      session.request(
        () => {
          throw new Error("renderer unavailable")
        },
        () => cleanup,
      ),
    ).resolves.toBeUndefined()
    expect(cleanup).toHaveBeenCalledOnce()
  })

  it("runs cancellation cleanup if registration cancels synchronously", async () => {
    const cleanup = vi.fn()
    const session = new SecretSession()
    const pending = session.request(
      () => true,
      (cancel) => {
        cancel()
        return cleanup
      },
    )
    await expect(pending).resolves.toBeUndefined()
    expect(cleanup).toHaveBeenCalledOnce()
  })

  it("still resolves if cancellation cleanup throws", async () => {
    const session = new SecretSession()
    const pending = session.request(
      () => true,
      () => () => {
        throw new Error("listener cleanup failed")
      },
    )
    session.submit(undefined)
    await expect(pending).resolves.toBeUndefined()
  })

  it("resolves immediately when notification is unavailable", async () => {
    await expect(
      new SecretSession().request(() => false),
    ).resolves.toBeUndefined()
  })
})
