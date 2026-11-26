import { readFileSync } from "node:fs"

const file = process.argv[2] ?? new URL("./tokens.css", import.meta.url)
const css = readFileSync(file, "utf8")

function block(selector) {
  const start = css.indexOf(`${selector} {`)
  if (start < 0) throw new Error(`no ${selector} block in tokens file`)
  const end = css.indexOf("}", start)
  const vars = {}
  for (const m of css
    .slice(start, end)
    .matchAll(/--([\w-]+):\s*oklch\(([^)]+)\)/g)) {
    const [l, c, h] = m[2].trim().split(/\s+/).map(Number)
    vars[m[1]] = { l, c, h }
  }
  return vars
}

function toLinearRgb({ l, c, h }) {
  const a = c * Math.cos((h * Math.PI) / 180)
  const b = c * Math.sin((h * Math.PI) / 180)
  const l_ = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3
  const m_ = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3
  const s_ = (l - 0.0894841775 * a - 1.291485548 * b) ** 3
  const clamp = (x) => Math.min(1, Math.max(0, x))
  return [
    clamp(4.0767416621 * l_ - 3.3077115913 * m_ + 0.2309699292 * s_),
    clamp(-1.2684380046 * l_ + 2.6097574011 * m_ - 0.3413193965 * s_),
    clamp(-0.0041960863 * l_ - 0.7034186147 * m_ + 1.707614701 * s_),
  ]
}

function luminance(color) {
  const [r, g, b] = toLinearRgb(color)
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}

function ratio(fg, bg) {
  const a = luminance(fg)
  const b = luminance(bg)
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05)
}

const text = 4.5
const ui = 3
const pairs = [
  ["foreground", "background", text],
  ["foreground", "card", text],
  ["muted-foreground", "background", text],
  ["muted-foreground", "card", text],
  ["muted-foreground", "muted", text],
  ["primary-foreground", "primary", text],
  ["link", "card", text],
  ["link", "background", text],
  ["sidebar-foreground", "sidebar", text],
  ["sidebar-accent-foreground", "sidebar-accent", text],
  ["destructive", "card", text],
  ["success", "success-bg", text],
  ["warning", "warning-bg", text],
  ["danger", "danger-bg", text],
  ["info", "info-bg", text],
  ["neutral", "neutral-bg", text],
  ["success", "card", text],
  ["warning", "card", text],
  ["danger", "card", text],
  ["info", "card", text],
  ["ring", "background", ui],
  ["ring", "card", ui],
  ["success-dot", "card", ui],
  ["warning-dot", "card", ui],
  ["danger-dot", "card", ui],
  ["info-dot", "card", ui],
  ["input", "card", ui],
  ["input", "background", ui],
  ["muted-foreground", "sidebar", text],
  ["muted-foreground", "popover", text],
  ["foreground", "accent", text],
  ["card-foreground", "card", text],
  ["popover-foreground", "popover", text],
  ["secondary-foreground", "secondary", text],
  ["accent-foreground", "accent", text],
  ["sidebar-primary-foreground", "sidebar-primary", text],
  ["destructive-foreground", "destructive", text],
  ["sidebar-ring", "sidebar", ui],
]

let failed = 0
for (const theme of [":root", ".dark"]) {
  const vars = { ...block(":root"), ...block(theme) }
  for (const [fg, bg, min] of pairs) {
    if (!vars[fg] || !vars[bg])
      throw new Error(`missing token in ${theme}: ${fg} or ${bg}`)
    const r = ratio(vars[fg], vars[bg])
    const ok = r >= min
    if (!ok) failed++
    console.log(
      `${theme.padEnd(6)} ${fg.padEnd(26)} on ${bg.padEnd(16)} ${r.toFixed(2).padStart(6)} ${ok ? "ok" : `FAIL < ${min}`}`,
    )
  }
}
process.exit(failed ? 1 : 0)
