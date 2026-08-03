# Docusaurus → Fumadocs Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the Docusaurus-based site in `sites/docs-devsy-sh` in place with a Next.js + Fumadocs site, preserving the marketing homepage, all 44 docs pages, all current URLs/redirects, and a working Netlify deploy — with zero broken links and zero paid third-party dependency for search. Alongside the tooling migration, produce a separate plan for auditing and updating the documentation content itself, since the migration touches every page anyway and research surfaced real staleness/accuracy issues worth tracking as follow-on work.

**Architecture:** Fumadocs is a set of libraries (`fumadocs-core`, `fumadocs-ui`, `fumadocs-mdx`) layered on a Next.js App Router app — not a standalone static-site generator like Docusaurus. Today, `sites/docs-devsy-sh` actually ships **two** things bundled by a Netlify build script: (1) a hand-written static marketing homepage at `public/index.html` copied to the site root, and (2) a Docusaurus-built docs app relocated into `build/docs/`. Fumadocs' own default project layout maps onto this split for free: a `(home)` route group renders `/`, and a `docs/[[...slug]]` route renders `/docs/*` — both in one Next.js app, no `basePath` hack or Netlify-side file-shuffling required. Content migrates from `pages/**/*.mdx` to `content/docs/**/*.mdx`, indexed via `fumadocs-mdx`'s `loader()`. Search uses Fumadocs' built-in ZBSearch (formerly Orama) engine — free, self-hosted, zero-config, works via a statically cached JSON index compatible with static export — replacing the current Algolia DocSearch integration.

**Tech Stack:** Next.js (App Router) + React + TypeScript, `fumadocs-core`, `fumadocs-ui`, `fumadocs-mdx`, Node.js ≥22 (repo has v24 installed — confirmed OK). Deployed as a static export (`output: 'export'`) on Netlify, replacing the current Docusaurus static build.

## Global Constraints

- **Migration happens in place inside `sites/docs-devsy-sh`** — no parallel directory. Because Docusaurus and Next.js have mutually incompatible `package.json`/build tooling, there is no way to run both side-by-side in the same folder; Task 2 is a hard cutover of the toolchain itself. There is no in-repo fallback during development — the safety net is git history (this work happens on a branch; the old Docusaurus site remains fully recoverable via `git log`/`git checkout`).
- Preserve the real homepage: `public/index.html`'s full marketing content (hero, features, agents, deploy-anywhere, how-it-works, services, contact form) — not the dead Docusaurus stub at `src/pages/index.js`, which renders nothing and can simply be deleted.
- Preserve the Netlify Forms contact form (`<form data-netlify="true" netlify-honeypot="bot-field">`) exactly as detected by Netlify's static-HTML form scanner — this only works if the literal form markup (name, data-netlify, netlify-honeypot, and all field `name` attributes) survives unchanged in the exported static HTML.
- Preserve every existing docs URL and anchor (`pages/**/*.mdx` file paths map 1:1 to Docusaurus doc IDs today, which are the URL slugs since `routeBasePath: "/"`, mounted under `/docs`). No link breakage.
- Preserve the existing redirect in `public/_redirects` (`/docs` → `/docs/what-is-devsy`, 301) and add a new one for the file move in Task 3.
- Search must be free with no third-party account required — use Fumadocs' built-in ZBSearch/Orama static engine, not Algolia (dropping the current Algolia DocSearch integration is an explicit, intentional decision here, not an oversight — see Task 7).
- Do not port `docusaurus-gtm-plugin` or `mdx-link-checker` npm packages — confirmed via repo grep that neither is referenced anywhere outside `package.json`; both are dead dependencies.
- Do not port `src/components/Step` / `src/components/Highlight` React components — confirmed via repo grep that no `.mdx` page actually renders `<Step>` or `<Highlight>` as JSX; all "Step N" text in content is plain Markdown headers/bold text. Dead code, not migrated.
- Keep "Edit this page" links pointing at the correct new path once content moves from `pages/` to `content/docs/` (Task 3).
- Netlify build automation (`.github/workflows/netlify-ops.yml`, `restore-netlify.yml`) restores production deploys via the Netlify API using `NETLIFY_AUTH_TOKEN`/`NETLIFY_SITE_ID` secrets — these don't need code changes, but Task 8 requires **manual confirmation from the user** of the Netlify dashboard's build settings (base directory, publish directory overrides), since I have no authenticated access to the Netlify account in this environment.

---

## File Structure

Final layout inside `sites/docs-devsy-sh` (Docusaurus-era files removed as Task 2 proceeds):

```
sites/docs-devsy-sh/
├── content/
│   └── docs/                       # migrated from pages/**/*.mdx (Task 3)
│       ├── meta.json
│       ├── what-is-devsy.mdx
│       ├── getting-started/
│       │   ├── meta.json
│       │   ├── install.mdx
│       │   ├── update.mdx
│       │   └── quickstart.mdx       # moved here from quickstart/ (Task 3)
│       ├── developing-in-workspaces/
│       ├── managing-machines/
│       ├── managing-providers/
│       ├── how-it-works/
│       ├── tutorials/
│       ├── developing-providers/
│       ├── troubleshooting/
│       └── fragments/               # partial-include mdx, excluded from nav
├── lib/
│   └── source.ts                    # fumadocs-mdx loader, baseUrl: '/docs' (Task 2)
├── app/
│   ├── layout.tsx                   # RootProvider, fonts (Task 2/4)
│   ├── (home)/
│   │   ├── layout.tsx
│   │   └── page.tsx                  # ported marketing homepage (Task 6)
│   ├── docs/
│   │   ├── layout.tsx                # DocsLayout w/ sidebar (Task 4)
│   │   └── [[...slug]]/
│   │       └── page.tsx              # renders a doc page (Task 2/4)
│   └── api/
│       └── search/
│           └── route.ts              # ZBSearch static index route (Task 7)
├── source.config.ts                  # defineConfig for fumadocs-mdx (Task 2)
├── next.config.mjs                   # withMDX + output: 'export' (Task 2)
├── public/
│   ├── _redirects                    # carried over + new redirect (Task 8)
│   └── docs/
│       └── media/                    # carried over from static/media (Task 5)
├── scripts/
│   └── check-links.ts                # next-validate-link CI script (Task 9)
├── netlify.toml                      # rewritten for static Next export (Task 8)
├── package.json
└── tsconfig.json
```

---

### Task 1: Record migration decisions

**Files:**
- Create: `docs/superpowers/plans/2026-08-03-docusaurus-to-fumadocs-migration-decisions.md`

**Interfaces:**
- Produces a written decision record consumed as context for Tasks 2, 6, 7, 8 — no code interface.

- [ ] **Step 1: Write the decisions file**

```markdown
# Docusaurus → Fumadocs Migration: Decisions

- **Routing:** No `basePath`. Use Fumadocs' default project layout: `app/(home)/page.tsx` renders `/`
  (the marketing homepage), `app/docs/[[...slug]]/page.tsx` renders `/docs/*` (the docs), both in one
  Next.js app. `lib/source.ts`'s `loader()` sets `baseUrl: '/docs'` to generate correct doc URLs/page tree.
  This matches current production structure without any Netlify-side file relocation.
- **Homepage:** Port `public/index.html` (the real marketing landing page) to `app/(home)/page.tsx` as a
  React component, not the dead Docusaurus stub `src/pages/index.js`. Preserve the Netlify Forms contact
  form markup exactly.
- **Netlify build:** Static export (`output: 'export'` in `next.config.mjs`), `netlify.toml` publishes the
  `out/` directory. Drop the current `mv`-based build-script relocation entirely — no longer needed.
- **Search:** Fumadocs' built-in ZBSearch (Orama) static search. Free, self-hosted, zero third-party
  account, compatible with static export via a cached JSON index. Replaces Algolia DocSearch — this is an
  intentional trade-off (loses Algolia's typo-tolerance/relevance tuning) accepted in favor of "free and
  best without ongoing third-party dependency."
- **Netlify dashboard settings:** Must be manually confirmed by the user before Task 8's deploy — this
  plan cannot verify the dashboard's base-directory/build-command overrides without Netlify credentials.
```

- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/plans/2026-08-03-docusaurus-to-fumadocs-migration-decisions.md
git commit -m "docs: record fumadocs migration decisions"
```

---

### Task 2: Cut over the toolchain in place — scaffold Fumadocs/Next.js

**Files:**
- Delete: `sites/docs-devsy-sh/docusaurus.config.js`, `sidebars.js`, `yarn.lock`, `src/pages/index.js`, `src/components/Step/`, `src/components/Highlight/`, `src/pages/styles.module.css`
- Modify: `sites/docs-devsy-sh/package.json` (full rewrite)
- Create: `sites/docs-devsy-sh/next.config.mjs`
- Create: `sites/docs-devsy-sh/source.config.ts`
- Create: `sites/docs-devsy-sh/lib/source.ts`
- Create: `sites/docs-devsy-sh/tsconfig.json`
- Create: `sites/docs-devsy-sh/app/layout.tsx`, `app/docs/layout.tsx`, `app/docs/[[...slug]]/page.tsx`, `app/(home)/page.tsx` (placeholder, replaced in Task 6)
- Create (temp, replaced in Task 3): `sites/docs-devsy-sh/content/docs/index.mdx`

**Interfaces:**
- Produces: `source` export from `lib/source.ts` (`{ getPage, getPages, pageTree }`), consumed by Tasks 3, 4, 7, 9.

This is the one unavoidable "big bang" step, since a single directory cannot run two incompatible build systems at once.

- [ ] **Step 1: Scaffold a fresh Fumadocs app in a throwaway sibling directory first, to get a known-good reference**

```bash
cd sites
npm create fumadocs-app -- _fumadocs-reference
```

Choose Framework = **Next.js**, Content source = **Fumadocs MDX**. This gives a working, correctly-wired reference app (App Router structure, working `mdx-components.tsx`, `RootProvider` setup) to copy from in the following steps — it is deleted at the end of this task, it is not the migration target itself.

- [ ] **Step 2: Verify the reference app builds**

```bash
cd _fumadocs-reference && npm install && npm run build
```

Expected: succeeds. This confirms the Node/npm environment can build a Fumadocs app before touching the real site.

- [ ] **Step 3: Remove Docusaurus-specific files from `sites/docs-devsy-sh`**

```bash
cd sites/docs-devsy-sh
git rm docusaurus.config.js sidebars.js yarn.lock
git rm -r src/pages/index.js src/pages/styles.module.css
git rm -r src/components/Step src/components/Highlight
```

Leave `src/css/custom.css` and `static/js/custom.js` in place for now — Task 4 audits and either ports or removes them.

- [ ] **Step 4: Copy the reference app's scaffold files into `sites/docs-devsy-sh`, adapting names/branding**

```bash
cd sites/docs-devsy-sh
cp ../_fumadocs-reference/next.config.mjs .
cp ../_fumadocs-reference/source.config.ts .
cp ../_fumadocs-reference/tsconfig.json .
cp -r ../_fumadocs-reference/lib .
cp -r ../_fumadocs-reference/app .
cp ../_fumadocs-reference/mdx-components.tsx . 2>/dev/null || true
```

Edit `lib/source.ts` to set `baseUrl: '/docs'` per Task 1's decision:

```ts
import { defineDocs } from 'fumadocs-mdx/macro';
import { loader } from 'fumadocs-core/source';

const docs = defineDocs({
  dir: 'content/docs',
});

export const source = loader({
  baseUrl: '/docs',
  source: docs.toFumadocsSource(),
});
```

Edit `next.config.mjs` to add static export:

```js
import { createMDX } from 'fumadocs-mdx/next';

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  output: 'export',
  images: {
    unoptimized: true, // required for static export
  },
};

export default withMDX(config);
```

- [ ] **Step 5: Rewrite `package.json`**

```json
{
  "name": "devsy-docs",
  "version": "0.0.0",
  "private": true,
  "scripts": {
    "dev": "next dev",
    "build": "next build",
    "start": "next start",
    "check-links": "tsx scripts/check-links.ts"
  },
  "dependencies": {
    "fumadocs-core": "latest",
    "fumadocs-mdx": "latest",
    "fumadocs-ui": "latest",
    "next": "latest",
    "react": "^19.0.0",
    "react-dom": "^19.0.0"
  },
  "devDependencies": {
    "@types/node": "latest",
    "@types/react": "latest",
    "@types/react-dom": "latest",
    "typescript": "latest"
  }
}
```

(Pin exact versions to whatever the reference app in Step 1 resolved to, rather than `latest`, once `npm install` has run — read them out of `_fumadocs-reference/package.json` and copy the literal resolved versions so this migration is reproducible.)

- [ ] **Step 6: Install and verify a minimal build**

```bash
npm install
mkdir -p content/docs
echo -e "---\ntitle: Placeholder\n---\n\nPlaceholder." > content/docs/index.mdx
npm run build
```

Expected: builds successfully with the placeholder content. This confirms the toolchain cutover itself works before content migration begins.

- [ ] **Step 7: Delete the throwaway reference app**

```bash
cd ..
rm -rf _fumadocs-reference
```

- [ ] **Step 8: Commit**

```bash
cd /Users/admin/.paseo/worktrees/0w7zktdb/punchy-lionfish
git add sites/docs-devsy-sh
git commit -m "feat(docs): cut over build toolchain from docusaurus to fumadocs/next.js"
```

---

### Task 3: Migrate content — restructure `pages/**` into `content/docs/**`

**Files:**
- Delete: `sites/docs-devsy-sh/pages/` (after copying)
- Create: `sites/docs-devsy-sh/content/docs/**` (44 files + `meta.json` per folder)
- Reference (for ordering, then deleted): `sites/docs-devsy-sh/sidebars.js` (already `git rm`'d in Task 2 — recover its content via `git show HEAD~1:sites/docs-devsy-sh/sidebars.js` when writing `meta.json` files)

**Interfaces:**
- Consumes: `source` from Task 2's `lib/source.ts`.

- [ ] **Step 1: Copy the raw content tree**

```bash
cd sites/docs-devsy-sh
cp -r pages/. content/docs/
rm content/docs/index.mdx  # remove Task 2's placeholder
```

- [ ] **Step 2: Recover the old sidebar ordering for reference**

```bash
git show HEAD~1:sites/docs-devsy-sh/sidebars.js > /tmp/old-sidebars.js
```

- [ ] **Step 3: Write `meta.json` files matching the old sidebar order**

Root `content/docs/meta.json`:

```json
{
  "pages": [
    "what-is-devsy",
    "getting-started",
    "developing-in-workspaces",
    "managing-machines",
    "managing-providers",
    "how-it-works",
    "tutorials",
    "developing-providers",
    "troubleshooting"
  ]
}
```

`content/docs/getting-started/meta.json` — **note:** `sidebars.js`'s "Getting Started" category interleaves `quickstart/quickstart` between `getting-started` docs, so `quickstart.mdx` must move folders to preserve nav order:

```json
{
  "title": "Getting Started",
  "pages": ["install", "update", "quickstart"]
}
```

```bash
mv content/docs/quickstart/quickstart.mdx content/docs/getting-started/quickstart.mdx
rmdir content/docs/quickstart
```

Remaining `meta.json` files, one per folder, titles/order taken from `/tmp/old-sidebars.js`:
- `developing-in-workspaces/meta.json` → `{"title": "Developing in a Workspace", "pages": [...15 ids, in listed order...]}`
- `managing-machines/meta.json` → `{"title": "Managing your Machines", "pages": ["what-are-machines", "manage-machines"]}`
- `managing-providers/meta.json` → `{"title": "Managing your Providers", "pages": ["what-are-providers", "add-provider", "set-source", "remove-provider", "rename-provider"]}`
- `how-it-works/meta.json` → `{"title": "Architecture", "pages": ["overview", "deploy-machines", "deploy-k8s", "deploying-workspaces"]}` — **drop** `building-workspaces` from this list: `sidebars.js` references doc id `how-it-works/building-workspaces`, but no such file exists in `pages/how-it-works/` (confirmed by directory listing). This is a pre-existing broken sidebar entry in the live Docusaurus site, unrelated to this migration — flag it to content owners rather than inventing the missing page.
- `tutorials/meta.json` → `{"title": "Tutorials", "pages": ["minikube-vscode-browser", "reduce-build-times-with-cache", "docker-provider-via-wsl", "podman-provider-setup"]}`
- `developing-providers/meta.json` → `{"title": "Developing Providers", "pages": ["quickstart", "options", "binaries", "agent", "driver"]}`
- `troubleshooting/meta.json` → `{"title": "Troubleshooting", "pages": ["troubleshooting", "linux-troubleshooting"]}`
- `fragments/meta.json` → `{"pages": []}` (excludes partials from nav — see Step 5)

- [ ] **Step 4: Convert Docusaurus-specific MDX syntax**

**(a) `@theme/Tabs`/`@theme/TabItem` → `fumadocs-ui/components/tabs`.** Only `content/docs/getting-started/install.mdx` and `update.mdx` use this (confirmed via grep):

```diff
- import Tabs from '@theme/Tabs';
- import TabItem from '@theme/TabItem';
- import CodeBlock from '@theme/CodeBlock';
+ import { Tab, Tabs } from 'fumadocs-ui/components/tabs';

- <Tabs values={[{label: 'macOS (Apple Silicon)', value: 'macarm64'}, ...]}>
- <TabItem value="macarm64">...</TabItem>
+ <Tabs items={['macOS (Apple Silicon)', 'macOS (Intel)', 'Windows', 'Linux (amd64)', 'Linux (arm64)']}>
+ <Tab value="macOS (Apple Silicon)">...</Tab>
```

Any `<CodeBlock>` inside becomes a plain fenced code block (Fumadocs auto-highlights fenced code; no import needed).

**(b) Docusaurus admonitions (`:::note`, `:::info`, `:::warning`) → Fumadocs `<Callout>`.** 17 files use these (confirmed via grep count). Fumadocs' `Callout` types: `info` (default), `warn`/`warning`, `error`, `success`, `idea` — no `note` type, map `:::note` → plain `<Callout>` (default info styling):

```diff
- :::note
- Some text.
- :::
+ <Callout>
+ Some text.
+ </Callout>

- :::warning
- Some text.
- :::
+ <Callout type="warn">
+ Some text.
+ </Callout>
```

`Callout` is auto-available via Fumadocs' default MDX components (`fumadocs-ui/mdx`'s `defaultMdxComponents`, wired in `mdx-components.tsx` from Task 2's scaffold copy) — no per-file import needed. Hand-edit all 17 files; admonition bodies vary too much in length/structure for a safe bulk regex, script-assist with a `sed` pass validated by manual diff review, not blind automation.

**(c) MDX fragment imports unchanged.** `import AddProvider from '../fragments/add-provider.mdx'` (in `quickstart.mdx`) and the two fragment imports in `minikube-vscode-browser.mdx` need no syntax change, only path correctness after the Step 3 quickstart move — verify these two files' relative import paths still resolve after the move.

- [ ] **Step 5: Verify `fragments/` is excluded from routing**

```bash
npm run dev
```

Visit `http://localhost:3000/docs/fragments/add-provider` and confirm it 404s (fragments are partials, not standalone pages).

- [ ] **Step 6: Verify every page renders**

Visit all 44 routes listed across the `meta.json` files. Confirm no MDX compile errors in the terminal, Tabs/Callouts render, and fragment-imported content shows inline correctly. Image 404s are expected here — fixed in Task 5.

- [ ] **Step 7: Delete the old `pages/` directory**

```bash
git rm -r pages
```

- [ ] **Step 8: Commit**

```bash
git add sites/docs-devsy-sh/content
git commit -m "feat(docs): migrate content from docusaurus pages to fumadocs content/docs"
```

---

### Task 4: Port the docs layout, theme, and branding

**Files:**
- Modify: `sites/docs-devsy-sh/app/layout.tsx`, `app/docs/layout.tsx`
- Create: `sites/docs-devsy-sh/lib/layout.shared.tsx`
- Modify/delete: `sites/docs-devsy-sh/src/css/custom.css`, `static/js/custom.js`
- Create: `sites/docs-devsy-sh/app/global.css`

**Interfaces:**
- Consumes: `source.pageTree` from `lib/source.ts`.
- Produces: `baseOptions()` consumed by `app/docs/layout.tsx` (this task) and `app/(home)/page.tsx` (Task 6).

- [ ] **Step 1: Port navbar/branding config from `docusaurus.config.js`'s `themeConfig`**

Old config: light-default color mode with toggle, respects OS preference; navbar with Devsy logo (light/dark variants) linking to `https://devsy.sh/`; one external GitHub icon link.

```tsx
// lib/layout.shared.tsx
import { type BaseLayoutProps } from 'fumadocs-ui/layouts/shared';
import Image from 'next/image';

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <>
          <Image src="/docs/media/devsy-logo-horizontal.svg" alt="devsy" width={120} height={24} className="dark:hidden" />
          <Image src="/docs/media/devsy-logo-horizontal-dark.svg" alt="devsy" width={120} height={24} className="hidden dark:block" />
        </>
      ),
      url: 'https://devsy.sh/',
    },
    links: [
      { type: 'icon', url: 'https://github.com/devsy-org/devsy', text: 'GitHub', icon: <GitHubIcon /> },
    ],
  };
}
```

Since assets live at `public/docs/media/*` (per Task 5), the `/docs/media/...` paths resolve correctly with no `basePath` involved — verify this once Task 5's asset copy is done (this task's own build check in Step 4 covers it).

- [ ] **Step 2: Wire color mode**

```tsx
// app/layout.tsx
<RootProvider theme={{ defaultTheme: 'light', enableSystem: true }}>
```

- [ ] **Step 3: Audit and port `src/css/custom.css` (600 lines)**

This is a research/judgment task, not a mechanical port — Docusaurus/Infima CSS variables (`--ifm-*`) have no automatic mapping to Fumadocs' Tailwind + `--fd-*` system:

1. Read through `custom.css` and bucket every rule: (a) color/spacing tokens with a direct `--fd-*` equivalent — remap; (b) component-specific overrides (navbar height, sidebar width) — find Fumadocs' Tailwind-class or CSS-var equivalent; (c) rules that only exist to support `static/js/custom.js`'s hand-rolled scroll-spy/active-TOC-highlighting — **drop these**, since Fumadocs' built-in sidebar/TOC already implements active-section highlighting natively.
2. Delete `static/js/custom.js` entirely once (c) is confirmed redundant by manual QA in Step 4 below.
3. Write the surviving, genuinely-custom rules into `app/global.css` as Tailwind `@layer` overrides.

Budget this as the highest-uncertainty step in the plan — treat it as a spike, not a fixed-effort task.

- [ ] **Step 4: Verify visually**

```bash
npm run dev
```

Toggle theme (light/dark logo swap), scroll a long doc page and confirm active-section highlighting still works via Fumadocs' native behavior, confirm no visibly broken spacing/branding vs. the current live site.

- [ ] **Step 5: Commit**

```bash
git add sites/docs-devsy-sh/app sites/docs-devsy-sh/lib
git rm sites/docs-devsy-sh/static/js/custom.js sites/docs-devsy-sh/src/css/custom.css
git commit -m "feat(docs): port branding, theme, and layout to fumadocs"
```

---

### Task 5: Migrate static assets

**Files:**
- Create: `sites/docs-devsy-sh/public/docs/media/` (copied from `static/media/`)
- Create: `sites/docs-devsy-sh/app/icon.svg` (favicon)
- Delete: `sites/docs-devsy-sh/static/`

**Interfaces:**
- None — filesystem only.

- [ ] **Step 1: Copy media assets to a path matching existing MDX references**

Existing `.mdx` content hardcodes `/docs/media/...` paths (8 files, confirmed via grep). Since this app has no `basePath`, place assets so that path resolves literally:

```bash
cd sites/docs-devsy-sh
mkdir -p public/docs/media
cp -r static/media/. public/docs/media/
cp static/media/devsy-favicon.svg app/icon.svg
```

- [ ] **Step 2: Verify in a real build (not just dev server)**

```bash
npm run build
find out -iname "devsy-flow.gif"
```

Confirm the file lands at `out/docs/media/devsy-flow.gif` — i.e., confirm `public/docs/media/x` really does serve at `/docs/media/x` with no `basePath` involved (this is standard Next.js `public/` behavior: files are served from site root at their path under `public/`, so `public/docs/media/x.gif` → `/docs/media/x.gif`, matching the hardcoded MDX references exactly with zero content rewrites needed).

- [ ] **Step 3: Delete the old static directory**

```bash
git rm -r static
```

- [ ] **Step 4: Commit**

```bash
git add sites/docs-devsy-sh/public sites/docs-devsy-sh/app/icon.svg
git commit -m "feat(docs): migrate static media assets"
```

---

### Task 6: Port the real marketing homepage

**Files:**
- Create: `sites/docs-devsy-sh/app/(home)/page.tsx`
- Create: `sites/docs-devsy-sh/app/(home)/layout.tsx`
- Create: `sites/docs-devsy-sh/app/(home)/home.css` (or Tailwind-equivalent styling)
- Delete: `sites/docs-devsy-sh/public/index.html`

**Interfaces:**
- Consumes: `baseOptions()` from Task 4.

This is real content work, not a stub — `public/index.html` is the actual production landing page for `https://devsy.sh/`.

- [ ] **Step 1: Port the static HTML structure to JSX**

Convert each section of `public/index.html` (header/nav, hero, features grid, agents grid, deploy-anywhere band, how-it-works steps, services grid, contact form, footer) into a single `app/(home)/page.tsx` React component. This is a mechanical HTML→JSX translation (convert `class` → `className`, self-close void elements, etc.) — preserve every section, every card, every piece of copy verbatim; this is marketing content, not something to paraphrase or trim.

- [ ] **Step 2: Preserve the Netlify Forms contact form exactly**

```tsx
<form name="contact" method="POST" data-netlify="true" netlify-honeypot="bot-field" className="contact-form">
  <input type="hidden" name="form-name" value="contact" />
  <p className="form-hidden">
    <label>Skip this field if you're human: <input name="bot-field" /></label>
  </p>
  {/* ...name, email, company, message fields, unchanged... */}
</form>
```

`data-netlify` and `netlify-honeypot` are non-standard attributes; React 16+ passes through any attribute containing a dash unmodified, so no special handling is needed beyond using them literally in JSX — but this must be verified against the actual static-exported HTML output (Step 4), since Netlify's form-detection bot scans the **built** HTML, not the JSX source, and any React-side attribute mangling would silently break form detection with no build error.

- [ ] **Step 3: Port the dark/light toggle to reuse the docs site's theme mechanism**

The current homepage has its own hand-rolled toggle (`localStorage.getItem("theme")`, `data-theme` attribute, independent of Docusaurus). Since Task 4 already wires `next-themes` via Fumadocs' `RootProvider`, reuse that instead of porting the duplicate hand-rolled script — call the same `useTheme()` hook (from `next-themes`) inside the homepage's toggle button, keeping one single theme mechanism across both the homepage and the docs, rather than two independent ones as today.

- [ ] **Step 4: Verify the exported static HTML preserves the form markup**

```bash
npm run build
grep -A2 'data-netlify' out/index.html
```

Confirm the output contains `data-netlify="true"` and `netlify-honeypot="bot-field"` as literal HTML attributes, and that all field `name` attributes (`name`, `email`, `company`, `message`, `bot-field`, `form-name`) are present unchanged — these exact names are what Netlify's form backend expects submissions to match.

- [ ] **Step 5: Visual verification**

```bash
npm run dev
```

Compare `http://localhost:3000/` side-by-side against the current live `https://devsy.sh/` (or a local static preview of `public/index.html` before deletion) — confirm layout, copy, and interactive elements (mobile nav toggle, theme toggle, smooth-scroll anchor links) all still work.

- [ ] **Step 6: Delete the old static homepage**

```bash
git rm sites/docs-devsy-sh/public/index.html
```

- [ ] **Step 7: Commit**

```bash
git add "sites/docs-devsy-sh/app/(home)"
git commit -m "feat(docs): port marketing homepage from static html to fumadocs app"
```

---

### Task 7: Verify built-in free search works end-to-end

**Files:**
- Create: `sites/docs-devsy-sh/app/api/search/route.ts` (only if the scaffold from Task 2 didn't already include it — check first)
- Modify: `sites/docs-devsy-sh/lib/layout.shared.tsx` (only if a non-default `SearchDialog` is needed — expected not to be)

**Interfaces:**
- Consumes: `source` from `lib/source.ts`.

Fumadocs' default `RootProvider` already wires ZBSearch/Orama search with zero extra configuration when a `source`-backed content collection exists. This task is primarily verification, not new integration work — the previous plan draft assumed Algolia required custom integration; the free default requires none.

- [ ] **Step 1: Confirm the scaffold already includes a working search route**

```bash
find app/api -iname "route.ts"
cat app/api/search/route.ts 2>/dev/null
```

If Task 2's scaffold copy included this file (it should have, since `npm create fumadocs-app` wires it by default for the Next.js + Fumadocs MDX template), skip to Step 3. If missing, create it:

```ts
// app/api/search/route.ts
import { source } from '@/lib/source';
import { createFromSource } from 'fumadocs-core/search/server';

export const { GET } = createFromSource(source);
```

- [ ] **Step 2: Confirm static-export compatibility**

Per Fumadocs' static-export docs: search works via a statically cached JSON file for static sites, requiring no server. Confirm the build produces a static search index:

```bash
npm run build
find out -iname "*.json" | xargs grep -l "static" 2>/dev/null | head -5
```

- [ ] **Step 3: Verify search works in the dev server**

```bash
npm run dev
```

Press ⌘K, type a known doc term (e.g. "workspace"), confirm results appear and clicking one navigates to the correct page.

- [ ] **Step 4: Verify search works against the static-exported build**

```bash
npx serve out -p 5000
```

Visit `http://localhost:5000`, press ⌘K, confirm search still returns results when served from static files (not the dev server) — this is the real production-equivalent check, since search behavior can differ between dev-server (server-backed) and static-export (JSON-file-backed) modes.

- [ ] **Step 5: Commit (only if Step 1 required creating the route file)**

```bash
git add sites/docs-devsy-sh/app/api
git commit -m "feat(docs): verify built-in zbsearch works with static export"
```

---

### Task 8: Rewrite the Netlify build pipeline

**Files:**
- Modify: `sites/docs-devsy-sh/public/_redirects`
- Modify: `sites/docs-devsy-sh/netlify.toml`

**Interfaces:**
- None — deployment config only.

**Blocker requiring user action:** I cannot authenticate to Netlify in this environment (`netlify-cli` is installed but unauthenticated, confirmed via `netlify status`). Before this task's build config is trusted in production, **ask the user to confirm, via the Netlify dashboard, whether this site has dashboard-level build setting overrides** (base directory, build command, publish directory) that would take precedence over — or conflict with — `netlify.toml`. Monorepo docs sites are frequently configured with a dashboard "base directory" pointing at `sites/docs-devsy-sh`; if so, no change needed there, but the build command/publish directory fields may also be overridden in the dashboard and silently ignore this file's settings.

- [ ] **Step 1: Update `_redirects`**

```
# Existing redirect
/docs                                           /docs/what-is-devsy                   301!

# New redirect for the quickstart move (Task 3, Step 3)
/docs/quickstart/quickstart                     /docs/getting-started/quickstart      301!
```

- [ ] **Step 2: Rewrite `netlify.toml` for static Next export**

```toml
[build]
publish = "out/"
command = "npm run build"

[build.processing]
skip_processing = false
[build.processing.html]
pretty_urls = true
[build.processing.css]
bundle = false
minify = false
[build.processing.js]
bundle = false
minify = false
[build.processing.images]
compress = true
```

This drops the old `mv`-based relocation script entirely (no longer needed — Task 1's routing decision means the app natively serves `/` and `/docs/*` without any post-build file shuffling) and drops the `rm -rf $OUT_DIR/fragments` no-op line (fragments are excluded from nav via `meta.json`, not deleted post-build).

- [ ] **Step 3: Verify locally with a static file server**

```bash
npm run build
npx serve out -p 5000
```

Visit `http://localhost:5000` and `http://localhost:5000/docs` — confirm the homepage and docs both render (note: `serve` doesn't interpret Netlify's `_redirects` syntax, so the `/docs` → `/docs/what-is-devsy` redirect itself needs the check below, not this step).

- [ ] **Step 4: Ask the user to confirm dashboard settings before merging (see blocker note above)**

Do not treat this task as fully done until the user has either confirmed the dashboard has no conflicting overrides, or updated them to match this `netlify.toml`.

- [ ] **Step 5: Commit**

```bash
git add sites/docs-devsy-sh/public/_redirects sites/docs-devsy-sh/netlify.toml
git commit -m "feat(docs): configure netlify static export build pipeline"
```

---

### Task 9: Set up link validation

**Files:**
- Create: `sites/docs-devsy-sh/scripts/check-links.ts`
- Modify: `sites/docs-devsy-sh/package.json`

**Interfaces:**
- Consumes: `source` from `lib/source.ts`.

This replaces the dead `mdx-link-checker` dependency and is the primary regression backstop for the whole migration, since Task 3's Callout/Tabs conversion can shift heading-derived anchor IDs.

- [ ] **Step 1: Install `next-validate-link`**

```bash
npm install -D next-validate-link tsx
```

- [ ] **Step 2: Write the check script**

```ts
// scripts/check-links.ts
import { type FileObject, printErrors, scanURLs, validateFiles } from 'next-validate-link';
import { source } from '@/lib/source';

async function checkLinks() {
  const scanned = await scanURLs({
    preset: 'next',
    populate: {
      'docs/[[...slug]]': source.getPages().map((page) => ({
        value: { slug: page.slugs },
      })),
    },
  });

  const files = await Promise.all(
    source.getPages().map(async (page): Promise<FileObject> => ({
      path: page.absolutePath,
      content: await page.data.getText('raw'),
      url: page.url,
      data: page.data,
    })),
  );

  const errors = await validateFiles(files, { scanned });
  printErrors(errors, true);
}

void checkLinks();
```

`"check-links": "tsx scripts/check-links.ts"` was already added to `package.json` in Task 2, Step 5 — confirm it's present.

- [ ] **Step 3: Run it against the migrated content and fix any breakage found**

```bash
npm run check-links
```

Read every reported error and fix each one — likely candidates: links using old relative paths broken by the `quickstart.mdx` move, or anchor links to headings whose auto-generated IDs changed due to the admonition→Callout conversion.

- [ ] **Step 4: Commit**

```bash
git add sites/docs-devsy-sh/scripts
git commit -m "feat(docs): add link validation via next-validate-link"
```

---

### Task 10: Final verification and deploy

**Files:**
- None — verification and deployment only.

**This task requires explicit user sign-off before any production deploy step**, per the guidance on actions affecting shared/production infrastructure — deploying is not something to do unilaterally even if all local checks pass.

- [ ] **Step 1: Run the full local verification suite**

```bash
cd sites/docs-devsy-sh
npm run build
npm run check-links
```

Expected: both succeed with zero errors.

- [ ] **Step 2: Search the repo for any remaining references to removed files**

```bash
cd /Users/admin/.paseo/worktrees/0w7zktdb/punchy-lionfish
grep -rln "docusaurus\|sidebars\.js\|src/pages/index" --include="*.yml" --include="*.yaml" --include="*.toml" --include="*.md" . | grep -v node_modules
```

Fix any hits found (none expected based on current research, but re-verify at execution time).

- [ ] **Step 3: Manual QA checklist against `npx serve out`**

- Homepage renders with all sections, contact form present with correct field names.
- All 44 docs pages load; nav order matches the old sidebar.
- Tabs render on `getting-started/install` and `update`.
- Callouts render wherever `:::note`/`:::warning` used to be.
- Search (⌘K) returns results from static export.
- Dark/light toggle works identically on homepage and docs.
- `/docs` redirects to `/docs/what-is-devsy`; `/docs/quickstart/quickstart` redirects to `/docs/getting-started/quickstart`.
- All images load (no 404s in browser console).

- [ ] **Step 4: Confirm Netlify dashboard settings with the user (per Task 8's blocker)**

Do not proceed to Step 5 without this confirmation.

- [ ] **Step 5: Open a PR and let Netlify's deploy-preview build it — do not deploy directly to production**

```bash
git push -u origin <branch-name>
gh pr create --title "Migrate docs site from Docusaurus to Fumadocs" --body "..."
```

Review the Netlify deploy-preview URL Netlify posts to the PR — this is the real end-to-end check (actual Netlify build environment, actual `_redirects` behavior, actual custom domain headers), not a substitute for Steps 1–3 but the final confirmation before merge.

- [ ] **Step 6: Merge only after explicit user approval of the preview**

---

### Task 11: Write a content review & update plan for all documentation

**Files:**
- Create: `docs/superpowers/plans/2026-08-03-docs-content-review.md`

**Interfaces:**
- Consumes: `content/docs/**/*.mdx` as it exists after Task 3 (this task can start as soon as Task 3 lands — it does not depend on Tasks 4–10, and should run in parallel with them, not after).

**This task produces a plan document, not content edits.** The actual doc rewrites are out of scope here — this is the planning pass that scopes and sequences that follow-on work, following the same `writing-plans` format as this document.

Migrating the tool is a good forcing function to also audit content accuracy, since every page gets hand-touched anyway for the Callout/Tabs syntax conversion in Task 3. Two concrete findings from this plan's own research make the case for a real audit, not a rubber-stamp:

- **Content staleness is uneven.** `git log` on the 44 doc files shows roughly half untouched since 2026-06-06 (`developing-providers/*`, `how-it-works/*`, `managing-machines/*`, `troubleshooting/*`, several `developing-in-workspaces/*` pages, etc.), while the codebase shipped real, doc-relevant features in the days just before this plan was written — workspace snapshots (`564708ee9`), SSH GPG-agent-forwarding fixes (`e46dc9276`), devcontainer platform/env propagation (`4dafedf2f`). The stale half is exactly where drift is most likely.
- **At least one concrete content bug already exists:** `tutorials/minikube-vscode-browser.mdx` has a command-output example containing literal placeholder octets (`xxx.yyy.zzz.qqq`) instead of a real or clearly-marked example IP.

- [ ] **Step 1: Inventory every doc page against current product behavior**

For each of the 44 pages (post-Task-3 paths under `content/docs/`), produce a table with: page path, last-git-modified date, and a one-line verdict — `verified-current`, `needs-update` (with a reason), or `needs-owner-input` (where correctness can't be determined by reading code alone, e.g. roadmap/pricing claims). Cross-reference each page's claims against the actual current CLI/config behavior (e.g. `devsy --help`, current `devcontainer.json` schema, current provider list) rather than trusting the doc's own text.

- [ ] **Step 2: Flag known-bad content found during this research pass**

Explicitly carry forward the two findings above (`minikube-vscode-browser.mdx`'s placeholder IP; the dead `how-it-works/building-workspaces` sidebar entry from Task 3) into this plan's task list as concrete, pre-identified fixes — don't make the next execution pass re-discover them.

- [ ] **Step 3: Scope and sequence the update work**

Group flagged pages into tasks by risk/effort (e.g. "broken commands or wrong flags" as highest priority, "stale screenshots/gifs" next, "prose polish" last), sized the same way this document sizes tasks — one task per page or tightly-related page cluster, each with a concrete verification step (re-run the documented command against current devsy, not just re-read the prose).

- [ ] **Step 4: Identify pages needing subject-matter input this plan can't resolve alone**

Some claims (pricing, roadmap timing, org-specific policy) aren't verifiable by reading the codebase — list these explicitly as questions for the user/content owners rather than guessing.

- [ ] **Step 5: Save and hand off**

Save as its own plan document (not folded into this one, since it's a distinct workstream with its own execution timeline) and follow this same plan's Execution Handoff pattern — offer subagent-driven or inline execution once written.

- [ ] **Step 6: Commit**

```bash
git add docs/superpowers/plans/2026-08-03-docs-content-review.md
git commit -m "docs: add content review and update plan"
```

---

## Limitations and Blockers (read before starting)

1. **In-place migration means no side-by-side fallback during development (Task 2).** Because Docusaurus and Next.js/Fumadocs cannot coexist in one `package.json`, Task 2 is a genuine cutover, not an incremental swap. The safety net is git history on this branch, not a parallel working site — if something goes wrong mid-migration, recovery means checking out an earlier commit, not comparing two live directories.

2. **The real homepage was previously undocumented and is a substantial port, not a stub (Task 6).** `public/index.html` is ~1500 lines of hand-built marketing HTML/CSS/JS including a Netlify Forms contact form — porting this to JSX correctly, including verifying Netlify's static-HTML form-detection still works post-export, is real content-preservation work with a real failure mode (a silently broken contact form) that's easy to miss without the explicit build-output grep check in Task 6, Step 4.

3. **CSS/theme port (Task 4, Step 3) is a research spike, not mechanical work.** The 600-line `custom.css` uses Docusaurus/Infima variables with no automatic mapping to Fumadocs' Tailwind system. Budget extra time; this plan intentionally doesn't pre-script the conversion since it requires visual judgment call-by-call.

4. **Dropping Algolia for the free built-in ZBSearch/Orama engine is an intentional trade-off, not a downgrade by accident (Task 7).** The current Algolia DocSearch setup has tuned relevance and typo-tolerance accumulated over time; the built-in free engine is simpler and has no ongoing third-party dependency, but search quality/UX may differ. Flagged explicitly per your "free and best" instruction — surfacing this trade-off rather than hiding it.

5. **A pre-existing broken sidebar reference was found (Task 3, Step 3).** `sidebars.js` lists `how-it-works/building-workspaces`, but no corresponding `.mdx` file exists in the current site — a bug in the current site, unrelated to this migration. Worth telling content owners regardless of migration outcome.

6. **Netlify dashboard settings cannot be verified from this environment (Task 8, Task 10).** `netlify-cli` is installed but unauthenticated here, and I have no `NETLIFY_AUTH_TOKEN`. This plan's `netlify.toml` rewrite is only correct if the dashboard doesn't have conflicting build-setting overrides — this must be confirmed by the user, not assumed.

7. **`next-validate-link` (Task 9) is the primary regression backstop for link/anchor breakage** introduced by the Callout/Tabs syntax conversion in Task 3. Do not skip it.

8. **Architectural shift for future maintainers.** Fumadocs is a Next.js library set, not a standalone SSG — a bigger shift than swapping to another content-focused tool. Whoever maintains this site going forward needs baseline Next.js App Router familiarity.

9. **Task 11 produces a plan, not content fixes — don't conflate the two workstreams.** The tooling migration (Tasks 1–10) must not block on, or be blocked by, the content audit — they're deliberately decoupled so the site migration can ship on its own timeline while content review proceeds separately (in parallel once Task 3 lands). Content correctness bugs found along the way (e.g. the placeholder IP in `minikube-vscode-browser.mdx`) are tracked for the follow-on plan, not fixed inline during migration, to keep the migration's diff reviewable and scoped to tooling.

---

## Self-Review Notes

- **Spec coverage:** every subsystem of the current site (marketing homepage, 44 docs pages + nav, theme/branding, search, redirects, custom components, link-checking, Netlify deploy) has a corresponding task, plus a dedicated task (11) scoping the content-accuracy audit requested alongside the tooling migration.
- **Placeholder scan:** no TBD/TODO left; genuine unknowns (CSS port judgment calls, Netlify dashboard settings, static-export search index format) are flagged as "verify/confirm before proceeding," matching what was actually found during research rather than glossed over.
- **Interface consistency:** `source` (from `lib/source.ts`) is the single shared interface referenced by name across Tasks 3, 4, 7, 9, 11 — matches Fumadocs' standard `loader()` return shape (`getPages()`, `pageTree`, `getPage()`) throughout.
