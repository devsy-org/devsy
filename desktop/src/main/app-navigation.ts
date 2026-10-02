import type { AppNavigationRequest } from "../shared/app-route.js"
import { isAppNavigationRequest, isAppRoute } from "../shared/app-route.js"
import { mainLog } from "./logging.js"

interface NavigationWebContents {
  send(channel: string, request: AppNavigationRequest): void
}

interface NavigationWindow {
  isDestroyed(): boolean
  isMinimized(): boolean
  restore(): void
  show(): void
  focus(): void
  webContents: NavigationWebContents
}

interface AppNavigationDependencies {
  getWindow(): NavigationWindow | null
  createWindow(): void
}

export class AppNavigationController {
  private nextId = 1
  private rendererReady = false
  private pending: AppNavigationRequest | null = null

  constructor(private readonly deps: AppNavigationDependencies) {}

  open(route?: string): void {
    if (route !== undefined) {
      if (!isAppRoute(route)) return
      this.pending = { id: this.nextId++, route }
      mainLog.debug(`[navigation] requested id=${this.pending.id}`)
    }

    const window = this.deps.getWindow()
    if (!window || window.isDestroyed()) {
      if (route !== undefined) mainLog.debug("[navigation] creating window")
      this.deps.createWindow()
      return
    }

    if (window.isMinimized()) window.restore()
    window.show()
    window.focus()
    if (route !== undefined) this.dispatch(window)
  }

  rendererDidStartLoading(): void {
    this.rendererReady = false
    if (this.pending) {
      mainLog.debug(`[navigation] loading; retaining id=${this.pending.id}`)
    }
  }

  rendererDidBecomeReady(): void {
    this.rendererReady = true
    const window = this.deps.getWindow()
    if (window && !window.isDestroyed()) this.dispatch(window)
  }

  navigationApplied(value: unknown): void {
    if (!isAppNavigationRequest(value)) return
    if (!this.rendererReady) return
    if (
      !this.pending ||
      value.id !== this.pending.id ||
      value.route !== this.pending.route
    ) {
      mainLog.debug(`[navigation] ignored stale acknowledgment id=${value.id}`)
      return
    }
    mainLog.debug(`[navigation] applied id=${value.id}`)
    this.pending = null
  }

  private dispatch(window: NavigationWindow): void {
    if (!this.rendererReady || !this.pending) return
    window.webContents.send("app-navigation-request", this.pending)
    mainLog.debug(`[navigation] dispatched id=${this.pending.id}`)
  }
}
