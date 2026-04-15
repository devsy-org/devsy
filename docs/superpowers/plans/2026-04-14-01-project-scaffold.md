# Plan 1: Project Scaffold

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Create a new `desktop-new/` directory with a working Tauri v2 + SvelteKit + shadcn-svelte + Tailwind CSS v4 project that compiles and shows a window.

**Architecture:** SvelteKit with `@sveltejs/adapter-static` generates a static site that Tauri serves in a webview. The Rust backend is minimal — just enough to launch the window. Frontend uses shadcn-svelte components with Tailwind v4.

**Tech Stack:** Tauri 2.10, SvelteKit 2, Svelte 5, shadcn-svelte, Tailwind CSS v4, TypeScript, Rust 2024 edition

---

### Task 1: Initialize SvelteKit project

**Files:**
- Create: `desktop-new/package.json`
- Create: `desktop-new/svelte.config.js`
- Create: `desktop-new/vite.config.ts`
- Create: `desktop-new/tsconfig.json`
- Create: `desktop-new/src/app.html`
- Create: `desktop-new/src/routes/+page.svelte`
- Create: `desktop-new/src/routes/+layout.svelte`

- [ ] **Step 1: Create the project directory**

```bash
mkdir -p desktop-new
cd desktop-new
```

- [ ] **Step 2: Create package.json**

```json
{
  "name": "devpod-desktop-new",
  "private": true,
  "version": "0.0.0",
  "type": "module",
  "scripts": {
    "dev": "vite dev",
    "build": "vite build",
    "preview": "vite preview",
    "check": "svelte-kit sync && svelte-check --tsconfig ./tsconfig.json"
  },
  "dependencies": {
    "@sveltejs/adapter-static": "^3.0.0",
    "@sveltejs/kit": "^2.0.0",
    "@sveltejs/vite-plugin-svelte": "^4.0.0",
    "svelte": "^5.0.0"
  },
  "devDependencies": {
    "svelte-check": "^4.0.0",
    "typescript": "^5.9.0",
    "vite": "^6.0.0"
  }
}
```

- [ ] **Step 3: Create svelte.config.js**

```js
import adapter from "@sveltejs/adapter-static";

/** @type {import('@sveltejs/kit').Config} */
const config = {
  kit: {
    adapter: adapter({
      fallback: "index.html",
    }),
  },
};

export default config;
```

- [ ] **Step 4: Create vite.config.ts**

```ts
import { sveltekit } from "@sveltejs/kit/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [sveltekit()],
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
  },
  envPrefix: ["VITE_", "TAURI_"],
  build: {
    target:
      process.env.TAURI_ENV_PLATFORM === "windows" ? "chrome105" : "safari14",
    minify: !process.env.TAURI_ENV_DEBUG ? "esbuild" : false,
    sourcemap: !!process.env.TAURI_ENV_DEBUG,
  },
});
```

- [ ] **Step 5: Create tsconfig.json**

```json
{
  "extends": "./.svelte-kit/tsconfig.json",
  "compilerOptions": {
    "allowJs": true,
    "checkJs": true,
    "esModuleInterop": true,
    "forceConsistentCasingInFileNames": true,
    "resolveJsonModule": true,
    "skipLibCheck": true,
    "sourceMap": true,
    "strict": true,
    "moduleResolution": "bundler",
    "paths": {
      "$lib": ["./src/lib"],
      "$lib/*": ["./src/lib/*"]
    }
  }
}
```

- [ ] **Step 6: Create src/app.html**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>DevPod</title>
    %sveltekit.head%
  </head>
  <body data-sveltekit-prerender="false">
    <div style="display: contents">%sveltekit.body%</div>
  </body>
</html>
```

- [ ] **Step 7: Create src/routes/+layout.svelte**

```svelte
<slot />
```

- [ ] **Step 8: Create src/routes/+page.svelte**

```svelte
<h1>DevPod</h1>
<p>Scaffold is working.</p>
```

- [ ] **Step 9: Install dependencies and verify build**

```bash
cd desktop-new
npm install
npm run build
```

Expected: Build succeeds, produces `desktop-new/build/` directory with `index.html`.

- [ ] **Step 10: Commit**

```bash
git add desktop-new/package.json desktop-new/svelte.config.js desktop-new/vite.config.ts desktop-new/tsconfig.json desktop-new/src/
git commit -m "feat(ui): scaffold SvelteKit project for new desktop app"
```

---

### Task 2: Add Tailwind CSS v4

**Files:**
- Create: `desktop-new/src/app.css`
- Modify: `desktop-new/src/routes/+layout.svelte`
- Modify: `desktop-new/package.json`

- [ ] **Step 1: Install Tailwind v4 and the Svelte plugin**

```bash
cd desktop-new
npm install -D tailwindcss @tailwindcss/vite
```

- [ ] **Step 2: Add the Vite plugin to vite.config.ts**

Add `tailwindcss` plugin to `vite.config.ts`:

```ts
import { sveltekit } from "@sveltejs/kit/vite";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [tailwindcss(), sveltekit()],
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
  },
  envPrefix: ["VITE_", "TAURI_"],
  build: {
    target:
      process.env.TAURI_ENV_PLATFORM === "windows" ? "chrome105" : "safari14",
    minify: !process.env.TAURI_ENV_DEBUG ? "esbuild" : false,
    sourcemap: !!process.env.TAURI_ENV_DEBUG,
  },
});
```

- [ ] **Step 3: Create src/app.css**

```css
@import "tailwindcss";
```

- [ ] **Step 4: Import CSS in layout**

```svelte
<script>
  import "../app.css";
</script>

<slot />
```

- [ ] **Step 5: Test Tailwind is working**

Update `src/routes/+page.svelte`:

```svelte
<div class="flex items-center justify-center h-screen bg-zinc-900">
  <h1 class="text-4xl font-bold text-white">DevPod</h1>
</div>
```

- [ ] **Step 6: Build and verify**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds. The built `index.html` includes Tailwind utility classes.

- [ ] **Step 7: Commit**

```bash
git add desktop-new/
git commit -m "feat(ui): add Tailwind CSS v4"
```

---

### Task 3: Add shadcn-svelte

**Files:**
- Create: `desktop-new/src/lib/components/ui/button/` (generated)
- Create: `desktop-new/src/lib/utils.ts`
- Modify: `desktop-new/package.json`
- Modify: `desktop-new/src/app.css`

- [ ] **Step 1: Install shadcn-svelte dependencies**

```bash
cd desktop-new
npx shadcn-svelte@next init
```

When prompted:
- Style: Default
- Base color: Zinc
- CSS variables: Yes

This will install `bits-ui`, `clsx`, `tailwind-merge`, `tailwind-variants`, and create config files.

- [ ] **Step 2: Add a Button component to verify**

```bash
cd desktop-new
npx shadcn-svelte@next add button
```

- [ ] **Step 3: Use the Button in the page**

Update `src/routes/+page.svelte`:

```svelte
<script>
  import { Button } from "$lib/components/ui/button";
</script>

<div class="flex flex-col items-center justify-center h-screen gap-4 bg-background">
  <h1 class="text-4xl font-bold text-foreground">DevPod</h1>
  <Button variant="default">Get Started</Button>
</div>
```

- [ ] **Step 4: Build and verify**

```bash
cd desktop-new
npm run build
```

Expected: Build succeeds with shadcn Button rendered.

- [ ] **Step 5: Commit**

```bash
git add desktop-new/
git commit -m "feat(ui): add shadcn-svelte component library"
```

---

### Task 4: Initialize Tauri v2 Rust backend

**Files:**
- Create: `desktop-new/src-tauri/Cargo.toml`
- Create: `desktop-new/src-tauri/src/main.rs`
- Create: `desktop-new/src-tauri/tauri.conf.json`
- Create: `desktop-new/src-tauri/build.rs`
- Create: `desktop-new/src-tauri/icons/` (generated)

- [ ] **Step 1: Install Tauri CLI**

```bash
cd desktop-new
npm install -D @tauri-apps/cli
```

- [ ] **Step 2: Initialize Tauri**

```bash
cd desktop-new
npx tauri init
```

When prompted:
- App name: DevPod
- Window title: DevPod
- Frontend dev URL: http://localhost:1420
- Frontend dist: ../build
- Frontend dev command: npm run dev
- Frontend build command: npm run build

- [ ] **Step 3: Replace generated Cargo.toml with our config**

```toml
[package]
name = "devpod-desktop-new"
version = "0.1.0"
description = "DevPod Desktop Application"
edition = "2024"

[build-dependencies]
tauri-build = { version = "2.10", features = [] }

[dependencies]
tauri = { version = "2.10", features = ["tray-icon", "image-ico", "image-png"] }
serde = { version = "1.0", features = ["derive"] }
serde_json = "1.0"
tokio = { version = "1", features = ["full"] }
log = "0.4"
tauri-plugin-log = "2.8"
tauri-plugin-shell = "2.3"
tauri-plugin-os = "2.3"
tauri-plugin-process = "2.3"
tauri-plugin-fs = "2.4"
tauri-plugin-dialog = "2.6"
tauri-plugin-clipboard-manager = "2.3"
tauri-plugin-opener = "2.5"
anyhow = "1.0"
thiserror = "1.0"

[features]
default = ["custom-protocol"]
custom-protocol = ["tauri/custom-protocol"]
```

- [ ] **Step 4: Replace generated main.rs with minimal app**

```rust
#![cfg_attr(
    all(not(debug_assertions), target_os = "windows"),
    windows_subsystem = "windows"
)]

use tauri::Manager;

fn main() {
    tauri::Builder::default()
        .plugin(tauri_plugin_log::Builder::new().build())
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_os::init())
        .plugin(tauri_plugin_process::init())
        .plugin(tauri_plugin_fs::init())
        .plugin(tauri_plugin_dialog::init())
        .plugin(tauri_plugin_clipboard_manager::init())
        .plugin(tauri_plugin_opener::init())
        .setup(|app| {
            let window = app.get_webview_window("main").unwrap();
            window.show().unwrap();
            Ok(())
        })
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
```

- [ ] **Step 5: Configure tauri.conf.json**

```json
{
  "build": {
    "beforeDevCommand": "npm run dev",
    "beforeBuildCommand": "npm run build",
    "frontendDist": "../build",
    "devUrl": "http://localhost:1420"
  },
  "bundle": {
    "active": true,
    "category": "DeveloperTool",
    "externalBin": ["bin/devpod"],
    "icon": [
      "icons/32x32.png",
      "icons/128x128.png",
      "icons/128x128@2x.png",
      "icons/icon.icns",
      "icons/icon.ico"
    ],
    "shortDescription": "Dev environments in any infra",
    "targets": "all"
  },
  "productName": "DevPod",
  "mainBinaryName": "DevPod Desktop",
  "version": "0.1.0",
  "identifier": "sh.loft.devpod.new",
  "plugins": {},
  "app": {
    "withGlobalTauri": false,
    "security": {
      "csp": null
    },
    "windows": [
      {
        "title": "DevPod",
        "width": 1200,
        "height": 800,
        "minWidth": 1000,
        "minHeight": 700,
        "visible": false,
        "resizable": true
      }
    ]
  }
}
```

- [ ] **Step 6: Create build.rs**

```rust
fn main() {
    tauri_build::build()
}
```

- [ ] **Step 7: Verify Rust compiles**

```bash
cd desktop-new/src-tauri
cargo check
```

Expected: Compiles without errors.

- [ ] **Step 8: Commit**

```bash
git add desktop-new/src-tauri/
git commit -m "feat(ui): initialize Tauri v2 Rust backend"
```

---

### Task 5: Verify full dev loop

**Files:**
- Modify: `desktop-new/package.json` (add tauri scripts)

- [ ] **Step 1: Add Tauri scripts to package.json**

Add to the `scripts` section:

```json
{
  "tauri": "tauri",
  "desktop:dev": "tauri dev",
  "desktop:build": "tauri build"
}
```

- [ ] **Step 2: Install Tauri API for frontend**

```bash
cd desktop-new
npm install @tauri-apps/api @tauri-apps/plugin-shell @tauri-apps/plugin-os @tauri-apps/plugin-process @tauri-apps/plugin-fs @tauri-apps/plugin-dialog @tauri-apps/plugin-clipboard-manager @tauri-apps/plugin-opener @tauri-apps/plugin-log
```

- [ ] **Step 3: Add Tauri detection to the page**

Update `src/routes/+page.svelte`:

```svelte
<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { onMount } from "svelte";

  let platform = $state("detecting...");

  onMount(async () => {
    try {
      const os = await import("@tauri-apps/plugin-os");
      platform = os.platform();
    } catch {
      platform = "browser (not in Tauri)";
    }
  });
</script>

<div class="flex flex-col items-center justify-center h-screen gap-4 bg-background">
  <h1 class="text-4xl font-bold text-foreground">DevPod</h1>
  <p class="text-muted-foreground">Platform: {platform}</p>
  <Button variant="default">Get Started</Button>
</div>
```

- [ ] **Step 4: Run the full dev loop**

```bash
cd desktop-new
npm run desktop:dev
```

Expected: Tauri window opens showing "DevPod" heading, detected platform, and a shadcn Button.

- [ ] **Step 5: Commit**

```bash
git add desktop-new/
git commit -m "feat(ui): complete scaffold with Tauri + SvelteKit + shadcn dev loop"
```
