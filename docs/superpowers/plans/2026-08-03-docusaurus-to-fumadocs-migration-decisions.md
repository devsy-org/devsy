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
