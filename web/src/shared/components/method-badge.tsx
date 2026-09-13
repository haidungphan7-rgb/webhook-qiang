import type React from 'react'
import { methodToColor } from '~/theme/color'
import { StatusBadge } from './status-badge'

/**
 * The HTTP method chip - the same "light" look as every other badge, but legible.
 *
 * A Mantine `light` badge always paints its text with step 6 of the palette (see the
 * comment on `BADGE_TEXT_SHADE`), and step 6 is a mid tone: measured against the 10%
 * tint it sits around 2-3:1, i.e. below WCAG AA for 12px text - POST was the worst at
 * 1.9:1. Rather than switching to solid chips (a much louder table) the text here is
 * taken from a darker step of the very same palette in light mode, and from a lighter
 * step in dark mode, where a dark text would be unreadable.
 *
 * Measured contrast against the chip's own background, light scheme: 4.8:1 (pink,
 * indigo) up to 9.9:1 (gray); dark scheme: all above 8:1.
 */
export function MethodBadge({ method }: { method: string }): React.JSX.Element {
  return <StatusBadge color={methodToColor(method)}>{method}</StatusBadge>
}
