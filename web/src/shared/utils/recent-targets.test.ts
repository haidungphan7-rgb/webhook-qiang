import { describe, expect, it } from 'vitest'
import type { AttemptOutcome } from '~/api/v1'
import { recentTargetsFrom } from './recent-targets'

const attempt = (target_url: string, outcome: AttemptOutcome, started_at: string) => ({
  target_url,
  outcome,
  started_at,
})

describe('recentTargetsFrom', () => {
  it('is empty when nothing has been replayed and nothing was typed', () => {
    expect(recentTargetsFrom([], [])).toEqual([])
  })

  it('offers the newest target first', () => {
    const targets = recentTargetsFrom([
      attempt('http://127.0.0.1:3000/old', 'success', '2026-09-13T10:00:00Z'),
      attempt('http://127.0.0.1:3000/new', 'timeout', '2026-09-13T11:00:00Z'),
    ])

    expect(targets.map((t) => t.url)).toEqual(['http://127.0.0.1:3000/new', 'http://127.0.0.1:3000/old'])
  })

  it('lists each address once, keeping its most recent result', () => {
    // Replaying the same local service ten times is the normal workflow; the list must
    // stay short and must show how the last try went.
    const targets = recentTargetsFrom([
      attempt('http://127.0.0.1:3000/hook', 'timeout', '2026-09-13T09:00:00Z'),
      attempt('http://127.0.0.1:3000/hook', 'success', '2026-09-13T12:00:00Z'),
      attempt('http://127.0.0.1:3000/hook', 'network_error', '2026-09-13T10:00:00Z'),
    ])

    expect(targets).toHaveLength(1)
    expect(targets[0]).toEqual({ url: 'http://127.0.0.1:3000/hook', outcome: 'success' })
  })

  it('carries the last result so the list can show whether the address worked', () => {
    const [blocked, ok] = recentTargetsFrom([
      attempt('http://10.0.0.5/h', 'blocked', '2026-09-13T12:00:00Z'),
      attempt('https://example.com/hook', 'http_error', '2026-09-13T11:00:00Z'),
    ])

    expect(blocked.outcome).toBe('blocked')
    expect(ok.outcome).toBe('http_error')
  })

  it('falls back to addresses typed in this browser, without inventing a result', () => {
    // localStorage is the only source when the server has no history (or was reset), and
    // it knows no outcome - null, not a guess.
    const targets = recentTargetsFrom([], ['http://127.0.0.1:3000/hook'])

    expect(targets).toEqual([{ url: 'http://127.0.0.1:3000/hook', outcome: null }])
  })

  it('puts replayed addresses before typed-only ones and never repeats one', () => {
    const targets = recentTargetsFrom(
      [attempt('https://example.com/hook', 'success', '2026-09-13T12:00:00Z')],
      ['https://example.com/hook', 'http://127.0.0.1:3000/hook'],
    )

    expect(targets.map((t) => t.url)).toEqual(['https://example.com/hook', 'http://127.0.0.1:3000/hook'])
  })

  it('caps the list', () => {
    const many = Array.from({ length: 9 }, (_, i) =>
      attempt(`http://127.0.0.1:300${i}/h`, 'success', `2026-09-13T1${i}:00:00Z`),
    )

    expect(recentTargetsFrom(many, [], 5)).toHaveLength(5)
  })

  it('drops blank entries instead of offering an empty chip', () => {
    expect(recentTargetsFrom([attempt('  ', 'success', '2026-09-13T12:00:00Z')], ['', '  '])).toEqual([])
  })
})
