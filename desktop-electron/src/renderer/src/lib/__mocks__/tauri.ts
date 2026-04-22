import { vi } from "vitest"

/**
 * Mock for IPC invoke function.
 * Tests can configure responses via mockInvoke.mockImplementation()
 */
export const mockInvoke = vi.fn()

/**
 * Mock for IPC listen function.
 * Returns a no-op unlisten by default.
 */
export const mockListen = vi.fn().mockResolvedValue(() => {})

// Mock the bridge module which commands.ts/terminal.ts/events.ts import from
vi.mock("$lib/ipc/bridge", () => ({
  invoke: mockInvoke,
  listen: mockListen,
}))

export function resetTauriMocks() {
  mockInvoke.mockReset()
  mockListen.mockReset().mockResolvedValue(() => {})
}
