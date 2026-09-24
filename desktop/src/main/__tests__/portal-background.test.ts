import { describe, expect, it, vi } from "vitest"

const { bus, requestBackground } = vi.hoisted(() => {
  const listeners = new Set<(message: unknown) => void>()
  const value = {
    on(_event: string, listener: (message: unknown) => void) {
      listeners.add(listener)
      return value
    },
    removeListener(_event: string, listener: (message: unknown) => void) {
      listeners.delete(listener)
      return value
    },
    emit(_event: string, message: unknown) {
      for (const listener of listeners) listener(message)
    },
    listenerCount: (_event: string) => listeners.size,
    getProxyObject: vi.fn(),
    disconnect: vi.fn(),
  } as {
    on: (event: string, listener: (message: unknown) => void) => typeof value
    removeListener: (
      event: string,
      listener: (message: unknown) => void,
    ) => typeof value
    emit: (event: string, message: unknown) => void
    listenerCount: (event: string) => number
    getProxyObject: ReturnType<typeof vi.fn>
    disconnect: ReturnType<typeof vi.fn>
  }
  return { bus: value, requestBackground: vi.fn() }
})

vi.mock("dbus-next", () => ({
  sessionBus: () => bus,
  Variant: class {
    constructor(
      public signature: string,
      public value: unknown,
    ) {}
  },
}))

import {
  parsePortalResponse,
  requestPortalBackground,
} from "../portal-background.js"

describe("portal background requests", () => {
  it("parses denial and cancellation responses as disabled", () => {
    expect(parsePortalResponse(1, undefined)).toEqual({
      background: false,
      autostart: false,
    })
  })

  it("subscribes before RequestBackground and accepts its returned path", async () => {
    const requestPath = "/org/freedesktop/portal/desktop/request/actual"
    bus.getProxyObject.mockImplementation(
      async (_name: string, path: string) => {
        if (path.endsWith("/desktop")) {
          return {
            getInterface: () => ({ RequestBackground: requestBackground }),
          }
        }
        throw new Error(`unexpected introspection: ${path}`)
      },
    )
    requestBackground.mockImplementationOnce(async () => {
      queueMicrotask(() =>
        bus.emit("message", {
          type: 4,
          interface: "org.freedesktop.portal.Request",
          member: "Response",
          path: requestPath,
          body: [
            0,
            { background: { value: true }, autostart: { value: true } },
          ],
        }),
      )
      return requestPath
    })

    await expect(
      requestPortalBackground({ reason: "test", autostart: true }),
    ).resolves.toEqual({ background: true, autostart: true })
    expect(bus.listenerCount("message")).toBe(0)
    expect(bus.disconnect).toHaveBeenCalled()
  })

  it("rejects a failed method call and still cleans up the bus", async () => {
    bus.getProxyObject.mockImplementationOnce(async () => ({
      getInterface: () => ({
        RequestBackground: vi.fn().mockRejectedValue(new Error("denied")),
      }),
    }))
    await expect(
      requestPortalBackground({ reason: "test", autostart: true }),
    ).rejects.toThrow("denied")
    expect(bus.listenerCount("message")).toBe(0)
    expect(bus.disconnect).toHaveBeenCalled()
  })
})
