<script lang="ts">
import { onMount, onDestroy } from "svelte"
import type { Terminal } from "@xterm/xterm"
import type { FitAddon } from "@xterm/addon-fit"
import {
  terminalWrite,
  terminalResize,
  terminalListSessions,
  onTerminalOutput,
  onTerminalExit,
} from "$lib/ipc/terminal.js"
import { theme } from "$lib/stores/settings.js"
import {
  getTerminalInstance,
  setTerminalInstance,
  type TerminalInstance,
} from "$lib/stores/terminal-instances.js"
import { get } from "svelte/store"

let {
  sessionId,
  active = true,
  onExit,
}: { sessionId: string; active?: boolean; onExit?: (exitCode?: number, signal?: number) => void } = $props()

let containerEl: HTMLDivElement | undefined = $state()

let term: Terminal | undefined
let fitAddon: FitAddon | undefined
let resizeObserver: ResizeObserver | undefined

const darkTheme = {
  background: "#1e1e2e",
  foreground: "#cdd6f4",
  cursor: "#f5e0dc",
  selectionBackground: "#585b70",
}

const lightTheme = {
  background: "#eff1f5",
  foreground: "#4c4f69",
  cursor: "#dc8a78",
  selectionBackground: "#ccd0da",
}

function isDark(): boolean {
  const current = get(theme)
  if (current === "system") {
    return window.matchMedia("(prefers-color-scheme: dark)").matches
  }
  return current === "dark"
}

function getAppTheme(): import("@xterm/xterm").ITheme {
  const probe = document.createElement("div")
  probe.className = "bg-background text-foreground"
  probe.style.display = "none"
  document.body.appendChild(probe)
  const computed = getComputedStyle(probe)
  const background = computed.backgroundColor
  const foreground = computed.color
  document.body.removeChild(probe)

  const selProbe = document.createElement("div")
  selProbe.className = "bg-muted"
  selProbe.style.display = "none"
  document.body.appendChild(selProbe)
  const selComputed = getComputedStyle(selProbe)
  const selectionBackground = selComputed.backgroundColor
  document.body.removeChild(selProbe)

  return {
    background: background || undefined,
    foreground: foreground || undefined,
    cursor: foreground || undefined,
    selectionBackground: selectionBackground || undefined,
  }
}

function resolveTheme(): import("@xterm/xterm").ITheme {
  const appTheme = getAppTheme()
  const fallback = isDark() ? darkTheme : lightTheme
  return {
    background: appTheme.background || fallback.background,
    foreground: appTheme.foreground || fallback.foreground,
    cursor: appTheme.cursor || fallback.cursor,
    selectionBackground: appTheme.selectionBackground || fallback.selectionBackground,
  }
}

onMount(async () => {
  if (!containerEl) return

  const existing = getTerminalInstance(sessionId)
  if (existing) {
    // Reattach existing terminal to the new container
    term = existing.term
    fitAddon = existing.fitAddon
    const el = term.element?.parentElement ?? term.element
    if (el) containerEl.appendChild(el)
    requestAnimationFrame(() => {
      fitAddon?.fit()
      term?.refresh(0, term!.rows - 1)
      term?.focus()
    })
  } else {
    // Register exit listener BEFORE async imports to avoid losing early exit events.
    // The PTY process may exit during the import window; without this, the event is lost
    // and the terminal appears open but is dead.
    const unlistenExit = await onTerminalExit((sid, exitCode, signal) => {
      if (sid === sessionId) {
        onExit?.(exitCode, signal)
      }
    })

    // Buffer output that arrives before xterm is ready
    const outputBuffer: Uint8Array[] = []
    const unlistenOutput = await onTerminalOutput((sid, data) => {
      if (sid === sessionId) {
        if (term) {
          term.write(data)
        } else {
          outputBuffer.push(data)
        }
      }
    })

    // Check if the session already exited before our listeners were registered.
    // The exit event may have been sent and lost during the IPC round-trip.
    const activeSessions = await terminalListSessions()
    if (!activeSessions.includes(sessionId)) {
      unlistenOutput()
      unlistenExit()
      // Signal connection failure with exit code -1 so parent can distinguish
      // from a normal session exit (where xterm was created and output was visible)
      onExit?.(-1)
      return
    }

    // Now safely do async imports — listeners are active, so no events are lost
    const [{ Terminal: XTerm }, { FitAddon: XFitAddon }] = await Promise.all([
      import("@xterm/xterm"),
      import("@xterm/addon-fit"),
    ])
    await import("@xterm/xterm/css/xterm.css")

    term = new XTerm({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "monospace",
      theme: resolveTheme(),
    })

    fitAddon = new XFitAddon()
    term.loadAddon(fitAddon)
    term.open(containerEl)
    fitAddon.fit()

    // Flush any output that arrived during async imports
    for (const data of outputBuffer) {
      term.write(data)
    }

    term.onData((data) => {
      const encoded = new TextEncoder().encode(data)
      terminalWrite(sessionId, Array.from(encoded))
    })

    const unsubscribeTheme = theme.subscribe(() => {
      if (term) {
        term.options.theme = resolveTheme()
      }
    })

    setTerminalInstance(sessionId, {
      term,
      fitAddon,
      unlistenOutput,
      unlistenExit,
      unsubscribeTheme,
    })
  }

  resizeObserver = new ResizeObserver(() => {
    if (fitAddon && term) {
      fitAddon.fit()
      term.refresh(0, term.rows - 1)
      terminalResize(sessionId, term.cols, term.rows)
    }
  })
  resizeObserver.observe(containerEl)
})

// Refit and focus when tab becomes active
$effect(() => {
  if (active && fitAddon && term) {
    requestAnimationFrame(() => {
      fitAddon?.fit()
      term?.refresh(0, term!.rows - 1)
      term?.focus()
    })
  }
})

onDestroy(() => {
  // Only disconnect the observer — keep the terminal instance alive
  resizeObserver?.disconnect()
})
</script>

<div bind:this={containerEl} class="h-full w-full p-2"></div>
