/** Mock for $app/navigation in vitest */
import { vi } from "vitest"

export const goto = vi.fn()
export const invalidate = vi.fn()
export const invalidateAll = vi.fn()
export const prefetch = vi.fn()
export const prefetchRoutes = vi.fn()
export const beforeNavigate = vi.fn()
export const afterNavigate = vi.fn()
