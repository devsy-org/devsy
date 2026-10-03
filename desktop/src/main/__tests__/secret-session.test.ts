// @vitest-environment node
import { describe, expect, it, vi } from "vitest"
import { SecretSession } from "../secret-session.js"

describe("SecretSession", () => {
  it("coordinates concurrent unlock requests without sending credentials back", async () => {
    const session = new SecretSession()
    const notify = vi.fn(() => true)
    const first = session.request(notify)
    const second = session.request(notify)
    expect(first).toBe(second)
    expect(notify).toHaveBeenCalledOnce()
    expect(session.submit("private credential")).toBeUndefined()
    await expect(first).resolves.toBe("private credential")
    const third = session.request(notify)
    session.submit(undefined)
    await expect(third).resolves.toBeUndefined()
    expect(notify).toHaveBeenCalledTimes(2)
  })
  it("does not hang when a renderer is unavailable", async () => {
    await expect(
      new SecretSession().request(() => false),
    ).resolves.toBeUndefined()
  })
})
