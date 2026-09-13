import { MantineProvider } from '@mantine/core'
import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MethodBadge } from './method-badge'
import { METHOD_COLORS, methodToColor } from '~/theme/color'
import { badgeTextContrast } from '~/test-utils/contrast'

function renderBadge(method: string, colorScheme: 'light' | 'dark' = 'light') {
  return render(
    <MantineProvider defaultColorScheme={colorScheme}>
      <MethodBadge method={method} />
    </MantineProvider>,
  )
}

/** The text sits in an inner label element; the colour is declared on the badge root. */
function chipStyle(method: string): string {
  return screen.getByText(method).closest('.mantine-Badge-root')?.getAttribute('style') ?? ''
}

describe('MethodBadge', () => {
  it('shows the method', () => {
    renderBadge('POST')

    expect(screen.getByText('POST')).toBeInTheDocument()
  })

  it('paints the text with a step Mantine cannot reach on its own', () => {
    // The whole reason this component exists: a `light` badge caps its text at step 6
    // (measured 1.9:1 for the old yellow POST). jsdom does not resolve var(), so the
    // assertion is on the declaration, not on the computed colour.
    renderBadge('POST')

    expect(chipStyle('POST')).toContain('--mantine-color-amber-8')
  })

  it('flips to a light step in dark mode', () => {
    // A dark step 8 on a dark background would be unreadable - measured ~2.7:1.
    renderBadge('POST', 'dark')

    expect(chipStyle('POST')).toContain('--mantine-color-amber-3')
  })

  it('keeps every method readable in both colour schemes', () => {
    // The two tests above pin *how* the colour is declared; this one pins what it has to
    // achieve. Measured on the chip's own 10% tint: step 6 (what Mantine would paint)
    // sits at 2.55-5.47:1, step 8/3 at 4.83-10.12:1 - so a palette tweak that quietly
    // moves a hue back under AA fails here instead of in a screenshot review.
    for (const method of Object.keys(METHOD_COLORS)) {
      const color = methodToColor(method)

      for (const scheme of ['light', 'dark'] as const) {
        expect(badgeTextContrast(color, 'light', scheme), `${method} in ${scheme}`).toBeGreaterThanOrEqual(4.5)
      }
    }
  })
})
