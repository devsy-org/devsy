# Design tokens

`tokens.css` holds the colour, status, type-scale, radius and motion variables that Devsy Desktop, the images site and Devsy Platform share. It defines variables only. Fonts, the base layer and components stay in each consumer.

## Files

- `tokens.css`: `:root` and `.dark` variables, the `dark` variant and the Tailwind `@theme inline` mapping. Import it after `@import "tailwindcss"`.
- `contrast.mjs`: reads `tokens.css` and checks each text pair at 4.5:1 and each control or indicator pair at 3:1, in light and dark. Status dots are checked on the card and on their tinted background. It also checks that the two motion durations exist. It exits 1 on any failure, on an `oklch()` value with alpha, and on a `.dark` value that is not `oklch()`.
- `contrast.test.mjs`: runs `contrast.mjs` against edited copies of `tokens.css` and checks each parser error and failure exit. Run it with `node --test design/tokens/*.test.mjs`.

## Motion

`--duration-fast` (150 ms) is for hover and press. `--duration-panel` (200 ms) is for panels and sheets. In Tailwind, use `duration-(--duration-fast)`. Consumers stop all motion under `prefers-reduced-motion`. The check compares the `:root` values as exact strings, so write `150ms` and `200ms`, not `0.15s`.

## Change a token

1. Edit `tokens.css`.
2. Run `mise run design:tokens:check` (or `node design/tokens/contrast.mjs`). Fix any pair marked FAIL.
3. Update every consumer that holds a copy.

The pre-commit hook `design-tokens-contrast` runs the check when a file in this folder changes.

## Rules

- `.dark` values are plain `oklch(l c h)`. The check cannot read `var()` aliases, hex or alpha there and exits 1. In `:root`, only alpha is rejected, and a value the check does not read is skipped.

- A status colour comes from a tone (`success`, `warning`, `danger`, `info`, `neutral`), never a hand-picked value.
- `--brand` (#7c5cff) is for the logo and large graphics. It is not a text colour. White text on it is 4.35:1.
- `--destructive` is for destructive buttons and errors that name an action. System-found problems use the `danger` tone.
- Add a pair to `contrast.mjs` when you add a token that carries text or a control edge.

## Consumers

Devsy repository consumers import this file. Other repositories keep a copy with a header that names the source commit and the file hash, and check it against this file.
