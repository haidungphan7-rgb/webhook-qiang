import { DEFAULT_THEME } from '@mantine/core'
import { expect } from 'vitest'
import { theme } from '~/theme'
import { BADGE_TEXT_SHADE } from '~/theme/color'

/**
 * Contrast measurement for the badge tests.
 *
 * It lives here so "is this chip readable?" has exactly one implementation: every badge
 * in the app paints its text through `StatusBadge`, and every badge test measures the
 * result with these helpers.
 */

/**
 * The palettes actually in effect, defaults included.
 *
 * `createTheme()` does not merge Mantine's DEFAULT_COLORS - that happens inside
 * MantineProvider - so reading `theme.colors` straight from ~/theme would miss every
 * palette the app did not redefine (gray, violet, ...).
 */
export const palettes = { ...DEFAULT_THEME.colors, ...theme.colors } as Record<string, readonly string[]>

const channels = (hex: string): number[] => [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16))

const linear = (c: number): number => (c / 255 <= 0.03928 ? c / 255 : ((c / 255 + 0.055) / 1.055) ** 2.4)

const luminance = (rgb: number[]): number => 0.2126 * linear(rgb[0]) + 0.7152 * linear(rgb[1]) + 0.0722 * linear(rgb[2])

/** `foreground` at `alpha` opacity over `background`, as hex. */
export function mix(foreground: string, background: string, alpha: number): string {
  const [fg, bg] = [channels(foreground), channels(background)]

  return `#${fg.map((v, i) => Math.round(v * alpha + bg[i] * (1 - alpha)).toString(16).padStart(2, '0')).join('')}`
}

/** WCAG 2.1 contrast ratio: 4.5 is the bar for the 12-13px text these badges use. */
export function contrastRatio(foreground: string, background: string): number {
  const [hi, lo] = [luminance(channels(foreground)), luminance(channels(background))].sort((a, b) => b - a)

  return (hi + 0.05) / (lo + 0.05)
}

/** The page behind a chip: Mantine's own `body` colour in each scheme. */
export function pageBackground(scheme: 'light' | 'dark'): string {
  return scheme === 'light' ? '#ffffff' : palettes.dark[7]
}

/**
 * The contrast of a `StatusBadge`'s text, measured against the chip's own background:
 * 10% of the palette over the page for `light`, the page itself for `outline`.
 */
export function badgeTextContrast(
  color: string,
  variant: 'light' | 'outline' | 'default' = 'light',
  scheme: 'light' | 'dark' = 'light',
): number {
  const [palette] = color.split('.')
  const page = pageBackground(scheme)
  const text = palettes[palette][BADGE_TEXT_SHADE[scheme]]

  return contrastRatio(text, variant === 'light' ? mix(palettes[palette][8], page, 0.1) : page)
}

/** The step a badge declared for its text: `--mantine-color-success-8` -> `8`. */
export function declaredStep(style: string): string | null {
  return style.match(/--mantine-color-[a-z]+-(\d+)\)/)?.[1] ?? null
}

/**
 * Asserts what actually matters about a badge: the text is not left on the step Mantine
 * caps a `light` badge at (6), and it clears WCAG AA in both colour schemes.
 *
 * The ratio - not the shade - is the assertion, so a future palette change has to keep
 * the bar instead of just keeping `8`.
 */
export function expectReadableBadge(style: string, color: string, variant: 'light' | 'outline' | 'default' = 'light'): void {
  expect(declaredStep(style), `${color} text step`).not.toBe('6')

  for (const scheme of ['light', 'dark'] as const) {
    expect(badgeTextContrast(color, variant, scheme), `${color} (${variant}) in ${scheme}`).toBeGreaterThanOrEqual(4.5)
  }
}
