import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { describe, expect, it } from 'vitest'
import { badgeTextContrast, expectReadableBadge } from '~/test-utils/contrast'
import { NEXT_STEP, OUTCOME_LIST, OUTCOME_META, OutcomeBadge, nextStepForBlocked, outcomeColor, type Outcome } from './outcome-badge'

function renderBadge(outcome: string, statusCode?: number, full = false, scheme: 'light' | 'dark' = 'light') {
  return render(
    <MantineProvider defaultColorScheme={scheme}>
      <OutcomeBadge outcome={outcome} statusCode={statusCode} full={full} />
    </MantineProvider>,
  )
}

/** The colour the badge actually paints its text with (declared on the badge root). */
function chipStyle(outcome: string, scheme: 'light' | 'dark'): string {
  const { unmount } = renderBadge(outcome, undefined, false, scheme)
  const label = OUTCOME_META[outcome as Outcome].short
  const style =
    screen.getByText(label).closest('.mantine-Badge-root')?.getAttribute('style') ?? ''

  unmount()

  return style
}

/** Measured on the chip's own background - see test-utils/contrast. */
function textContrast(outcome: string, scheme: 'light' | 'dark'): number {
  const meta = OUTCOME_META[outcome as Outcome]

  return badgeTextContrast(meta.color, meta.variant, scheme)
}

describe('OutcomeBadge', () => {
  it('labels every outcome in Chinese', () => {
    const cases: Array<[string, string]> = [
      ['success', '成功'],
      ['http_error', '非 2xx'],
      ['timeout', '超时'],
      ['network_error', '网络错误'],
      ['blocked', '被拦截'],
      ['rate_limited', '已限流'],
    ]

    for (const [outcome, label] of cases) {
      const { unmount } = renderBadge(outcome)

      expect(screen.getByText(label)).toBeInTheDocument()
      unmount()
    }
  })

  it('distinguishes the two "got a response" cases', () => {
    // The task requires 2xx and non-2xx to be told apart; a shared "failed" label would
    // lose the difference between "the target answered" and "we never got there".
    const { unmount } = renderBadge('http_error', 500)
    expect(screen.getByText('非 2xx · 500')).toBeInTheDocument()
    unmount()

    renderBadge('network_error')
    expect(screen.getByText('网络错误')).toBeInTheDocument()
  })

  it('renders an icon for every outcome so colour is not the only signal', () => {
    const { container } = renderBadge('blocked')
    expect(container.querySelector('svg')).not.toBeNull()
  })

  it('gives every entry in the table a short Chinese label and an icon', () => {
    // Driven by OUTCOME_META itself rather than a hard coded list: adding an outcome to
    // the table without a label or an icon fails here instead of in a screenshot review.
    const entries = Object.entries(OUTCOME_META)

    expect(entries.length).toBeGreaterThanOrEqual(6)

    for (const [outcome, meta] of entries) {
      const { container, unmount } = renderBadge(outcome)

      expect(screen.getByText(meta.short)).toBeInTheDocument()
      expect(container.querySelector('svg')).not.toBeNull()

      unmount()
    }
  })

  it('derives outcomeColor from the same table the badge uses', () => {
    // The badge and the card border are two renderings of one verdict; a second, hand
    // written colour chain would eventually disagree with the badge it sits next to.
    for (const [outcome, meta] of Object.entries(OUTCOME_META)) {
      expect(outcomeColor(outcome, 'light')).toBe(`var(--mantine-color-${meta.color.replace('.', '-')})`)
    }
  })

  it('keeps every outcome readable in both colour schemes', () => {
    // The whole reason OutcomeBadge paints its own text: `variant="light"` is hardcoded
    // to step 6, where these measured 2.17 / 2.68 / 3.01:1. The assertion is on the
    // measured ratio, so changing a palette has to keep the bar - not just the shade.
    for (const outcome of Object.keys(OUTCOME_META)) {
      expect(textContrast(outcome, 'light'), `${outcome} in light`).toBeGreaterThanOrEqual(4.5)
      expect(textContrast(outcome, 'dark'), `${outcome} in dark`).toBeGreaterThanOrEqual(4.5)
    }
  })

  it('never leaves the text on the step Mantine caps a light badge at', () => {
    // If somebody "simplifies" this back to `variant="light"` the badges look identical
    // in a screenshot review and quietly drop to ~2:1 again - so pin the step itself.
    for (const outcome of Object.keys(OUTCOME_META)) {
      for (const scheme of ['light', 'dark'] as const) {
        const style = chipStyle(outcome, scheme)

        expect(style, `${outcome} in ${scheme}`).toMatch(/--mantine-color-[a-z]+-(8|3)\)/)
        expectReadableBadge(style, OUTCOME_META[outcome as Outcome].color, OUTCOME_META[outcome as Outcome].variant)
      }
    }
  })

  it('never prints a raw snake_case value for an unknown outcome', () => {
    // The server sends snake_case English; dropping that into a Chinese screen reads as
    // a bug. The value is still shown, but labelled.
    renderBadge('something_new')
    expect(screen.getByText(/未知结果/)).toBeInTheDocument()
    expect(screen.getByText(/something_new/)).toBeInTheDocument()
  })

  it('explains the next step instead of only reporting a failure', () => {
    expect(NEXT_STEP.timeout).toContain('超时')
    expect(NEXT_STEP.blocked).toContain('未发出')
  })

  it('lists six outcomes and keeps the seventh out of the history guide', () => {
    // `invalid_target` is stored as `blocked`, so listing it would promise a state that
    // never shows up in the replay history.
    expect(OUTCOME_LIST).toHaveLength(6)
    expect(OUTCOME_LIST).not.toContain('invalid_target')
    expect(OUTCOME_LIST).toContain('rate_limited')
  })

  it('still renders the seventh outcome for the error card', () => {
    renderBadge('invalid_target', undefined, true)

    expect(screen.getByText('目标地址不合法，服务端拒绝发起重放')).toBeInTheDocument()
    // The remedy is "fix how you wrote it", not "use a public address".
    expect(NEXT_STEP.invalid_target).toContain('改写法')
    expect(NEXT_STEP.invalid_target).not.toContain('公网地址')
  })

  it('tells rate limiting apart from a blocked target', () => {
    // Both mean "nothing was sent" - the remedies are opposites.
    expect(NEXT_STEP.rate_limited).toContain('倒计时')
    expect(NEXT_STEP.rate_limited).not.toContain('--replay-allow-host')
    expect(NEXT_STEP.blocked).toContain('--replay-allow-host')
    expect(NEXT_STEP.blocked).not.toContain('倒计时')
    expect(outcomeColor('rate_limited', 'light')).not.toBe(outcomeColor('blocked', 'light'))
  })

  it('never tells the user to move their service to the public internet', () => {
    // The target is usually the user's own machine: "use a public address" is both wrong
    // and something they cannot act on. The remedy is a startup flag.
    for (const step of Object.values(NEXT_STEP)) {
      expect(step).not.toContain('请改用 http(s) 公网地址')
    }
  })

  it('exposes one colour mapping as a usable CSS variable', () => {
    // Mantine names variables with a hyphen; a dot here would make the browser drop the
    // whole declaration (which is exactly how the colour bar went missing once).
    // The scheme is a required argument on purpose: a step 8 bar on a dark background
    // measures ~2.7:1, so "one step for both schemes" is not an option.
    expect(outcomeColor('success', 'light')).toBe('var(--mantine-color-success-8)')
    expect(outcomeColor('success', 'dark')).toBe('var(--mantine-color-success-3)')
    expect(outcomeColor('rate_limited', 'light')).toBe('var(--mantine-color-amber-8)')
    expect(outcomeColor('blocked', 'light')).toBe('var(--mantine-color-gray-8)')
    expect(outcomeColor('unknown', 'light')).toBe('var(--mantine-color-gray-8)')
  })
})

describe('nextStepForBlocked', () => {
  const settings = (hosts: string[] = [], allowPrivate = false) => ({
    replay_allow_hosts: hosts,
    replay_allow_private: allowPrivate,
  })

  it('names the host the user actually used, so the command can be copied', () => {
    // This is the whole point: the generic text can only say "<host>", the result card
    // knows the address.
    const step = nextStepForBlocked('http://127.0.0.1:3000/webhook', settings())

    expect(step).toContain('--replay-allow-host 127.0.0.1')
    expect(step).toContain('--replay-allow-private')
    expect(step).not.toContain('请改用 http(s) 公网地址')
  })

  it('asks only for the host when the address is public but not listed', () => {
    // A public host outside the list needs the allow list, not the private flag.
    const step = nextStepForBlocked('https://example.com/hook', settings(['127.0.0.1'], true))

    expect(step).toContain('--replay-allow-host example.com')
    expect(step).not.toContain('--replay-allow-private')
  })

  it('says the address is already allowed when this instance allows it', () => {
    // The attempt was refused once, but the settings may have changed since - the answer
    // comes from the server's current settings, not from the stored attempt.
    const step = nextStepForBlocked('http://127.0.0.1:3000/webhook', settings(['127.0.0.1'], true))

    expect(step).toContain('已被当前实例放行')
  })

  it('falls back to the generic remedy when the browser cannot know', () => {
    // A name may resolve anywhere; do not invent a verdict for it.
    expect(nextStepForBlocked('https://example.com/hook', settings())).toBe(NEXT_STEP.blocked)
  })
})
