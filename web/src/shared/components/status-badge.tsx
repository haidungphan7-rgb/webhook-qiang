import type React from 'react'
import { Badge, type BadgeProps, useComputedColorScheme } from '@mantine/core'
import { BADGE_TEXT_SHADE } from '~/theme/color'

/**
 * A badge whose text is legible - the single primitive behind every coloured chip in the
 * app (method chips, outcome badges, status badges).
 *
 * Why this exists: Mantine's `light` variant hardcodes the text colour to step 6 of the
 * palette (`default-variant-colors-resolver.mjs` returns `min(shade, 6)`), so passing
 * `color="green.8"` only deepens the 10% background and leaves the text at step 6 - which
 * measured 1.75:1 to 3.01:1 depending on the hue. A state indicator nobody can read is
 * worse than none, because it reads as "no indicator". The text is therefore painted
 * here, from step 8 in light mode and step 3 in dark mode (see `BADGE_TEXT_SHADE`).
 *
 * Everything else - variant, icon, radius, size - stays Mantine's, so this is still a
 * plain Mantine badge from the outside.
 */
/**
 * Mantine's `BadgeProps` does not cover the plain span attributes a call site may need
 * (`tabIndex`, `aria-label`), so they are added here - they are forwarded untouched.
 */
type StatusBadgeProps = Omit<BadgeProps, 'color' | 'variant'> &
  Omit<React.ComponentPropsWithoutRef<'span'>, 'color'> & {
    color: string
    variant?: BadgeProps['variant']
  }

export function StatusBadge({
  color,
  variant = 'light',
  children,
  ...others
}: StatusBadgeProps): React.JSX.Element {
  const scheme = useComputedColorScheme('light')
  const [palette] = color.split('.')

  return (
    <Badge
      {...others}
      color={color}
      variant={variant}
      style={{ color: `var(--mantine-color-${palette}-${BADGE_TEXT_SHADE[scheme]})` }}
    >
      {children}
    </Badge>
  )
}
