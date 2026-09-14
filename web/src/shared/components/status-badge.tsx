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
 *
 * `component` is added for the same reason: the polymorphic prop lives on Badge's function
 * signature (`PolymorphicProps`), not on `BadgeProps`, so it cannot pass through `others`
 * without being declared. Passing `component="span"` renders the root as a span, which is
 * the only way a badge may sit inside a `Text` - the default div root inside a <p> is
 * invalid HTML and React reports it as a hydration error.
 */
type StatusBadgeProps = Omit<BadgeProps, 'color' | 'variant'> &
  Omit<React.ComponentPropsWithoutRef<'span'>, 'color'> & {
    color: string
    variant?: BadgeProps['variant']
    /** Polymorphic root element; `"span"` for badges used inline in text. */
    component?: React.ElementType
  }

export function StatusBadge({
  color,
  variant = 'light',
  // Destructured (not spread) and passed explicitly below: Badge's polymorphic prop union
  // constrains `component` per element tag and rejects a forwarded `ElementType` (the union
  // distributes over every intrinsic tag and no single member accepts all of them). The
  // value is only ever a root tag chosen at a call site (`'span'` for inline-in-text use),
  // so the cast below is safe and keeps StatusBadge renderable inside a `Text`.
  component = 'div',
  children,
  ...others
}: StatusBadgeProps): React.JSX.Element {
  const scheme = useComputedColorScheme('light')
  const [palette] = color.split('.')

  return (
    <Badge
      {...others}
      // See the destructuring note above: without this cast the polymorphic union
      // matches no member for a forwarded ElementType.
      component={component as 'span'}
      color={color}
      variant={variant}
      style={{ color: `var(--mantine-color-${palette}-${BADGE_TEXT_SHADE[scheme]})` }}
    >
      {children}
    </Badge>
  )
}
