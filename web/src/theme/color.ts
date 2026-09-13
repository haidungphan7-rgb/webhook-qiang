import type { MantineColor } from '@mantine/core'

/**
 * The palette behind each HTTP method.
 *
 * Hues are the ones everybody already reads correctly (a green GET, a red DELETE), and
 * they are kept as they were. Two of them moved to the theme's own TDesign palette -
 * green -> `success`, orange -> `warning` - because Mantine's default `green`/`orange`
 * have no step dark enough to carry text on a tinted background (measured: 3.9:1 and
 * 3.9:1 at their darkest, i.e. below AA however you shade them).
 */
// Exported so the badge tests can walk every method without keeping a second copy of
// this list (a copy would drift the day somebody adds a method).
export const METHOD_COLORS: Readonly<Record<string, string>> = {
  GET: 'success',
  HEAD: 'success',
  POST: 'amber',
  PUT: 'brand',
  PATCH: 'violet',
  DELETE: 'error',
  OPTIONS: 'warning',
  TRACE: 'pink',
  CONNECT: 'indigo',
}

/**
 * The step a "light" badge's text is painted with, per colour scheme. Shared by every
 * badge in the app (method chips and outcome badges).
 *
 * Why this exists: Mantine's `light` variant hard caps the text colour at step 6 -
 * `default-variant-colors-resolver.mjs` returns `var(--mantine-color-{c}-min(shade,6))`
 * no matter which shade you pass, so `color="yellow.8"` only deepens the 10% background
 * tint and leaves the text at step 6 (measured 1.75:1 for yellow). Passing a darker step
 * therefore cannot make a `light` badge legible; the text has to be painted separately.
 *
 * Step 8 in light mode (4.8:1 and up, measured), step 3 in dark mode, where a dark text
 * on a dark background would be unreadable.
 */
export const BADGE_TEXT_SHADE: Record<'light' | 'dark', number> = { light: 8, dark: 3 }

/**
 * The background tint for a method chip, as `palette.step`.
 *
 * Only the tint (10% alpha) comes from this - the readable text colour is applied by
 * `MethodBadge`.
 */
export const methodToColor = (
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS' | 'CONNECT' | 'TRACE' | string,
): MantineColor => `${METHOD_COLORS[method.trim().toUpperCase()] ?? 'gray'}.8`
