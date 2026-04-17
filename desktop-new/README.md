# DevPod Desktop (New)

Rewrite of the DevPod desktop app using **Tauri v2 + SvelteKit + shadcn-svelte**.

## Stack

| Layer | Tech |
|-------|------|
| Shell | [Tauri v2](https://tauri.app/) (Rust) |
| Frontend | [SvelteKit](https://svelte.dev/) (SPA via `adapter-static`) |
| Components | [shadcn-svelte](https://next.shadcn-svelte.com/) (bits-ui + Tailwind v4) |
| State | Svelte 5 runes (`$state`, `$derived`, `$effect`) + stores |
| IPC | Tauri `invoke` commands with mock layer for browser dev |
| Terminal | xterm.js + Tauri PTY backend |

## Getting Started

```bash
cd desktop-new
npm install

# Browser-only dev (mock IPC, no Rust compilation)
npm run dev          # http://localhost:1420

# Full Tauri dev (requires Rust toolchain)
npm run desktop:dev

# Type-check
npm run check

# Tests
npm run test
```

## Project Structure

```
desktop-new/
  src/
    routes/(app)/       # App pages (dashboard, workspaces, providers, etc.)
    routes/splashscreen/ # Tauri splash screen (static, no JS logic)
    lib/
      components/
        ui/             # shadcn-svelte primitives (31 components)
        layout/         # Sidebar, Breadcrumbs, CommandPalette, etc.
        provider/       # ProviderCard, ProviderIcon, ProviderSheet
        workspace/      # CreateWorkspaceSheet, etc.
      ipc/              # Tauri command wrappers + mock layer
      stores/           # Svelte stores (workspaces, providers, machines, etc.)
      types/            # TypeScript interfaces
    app.html            # HTML shell
    app.css             # Tailwind v4 entry
  src-tauri/
    src/
      commands/         # Tauri IPC command handlers
      daemon/           # CLI runner, state, filesystem watcher
      persistence/      # SQLite audit log + log store
      terminal/         # PTY management
      macos.rs          # macOS WKWebView workarounds
    tauri.conf.json     # Tauri window & plugin config
```

## Architecture Notes

**IPC mock layer** — All Tauri commands go through `src/lib/ipc/commands.ts`, which delegates to either real Tauri `invoke` calls or mock implementations (in `src/lib/ipc/mock.ts`). This lets you develop the UI in a browser without compiling Rust. The `isTauri()` check detects the runtime.

**Stores** — Each domain (workspaces, providers, machines, contexts) has an `init*` / `destroy*` lifecycle called from the app layout's `onMount`/`onDestroy`. Stores poll the CLI for state and emit events.

**Daemon watcher** — The Rust backend watches `~/.devpod` for filesystem changes and polls the DevPod CLI for state. Changes are pushed to the frontend via Tauri events.

**Provider icons** — `ProviderIcon.svelte` maps provider names to SVG brand logos (AWS, GCP, Azure, Docker, Kubernetes). Unknown providers get a generic fallback.

## Known Issues

**macOS WKWebView first-click bug** — SvelteKit's `goto()` and `<a href>` navigation silently fail on the first click in Tauri's WKWebView. Root cause is SSR hydration — the Tauri docs require `ssr = false` but this currently causes a black screen in dev mode. See `app.html` for full details and issue links.

## Adding a UI Component

```bash
npx shadcn-svelte@next add <component>
```

Components land in `src/lib/components/ui/`. They use bits-ui for headless behavior and Tailwind v4 for styling.
