import { MantineProvider } from '@mantine/core'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { encodeBody, type EventDetail, type ServerSettings } from '~/api/v1'
import { expectReadableBadge } from '~/test-utils/contrast'
import { EventScreen } from './screen'

const EVENT_ID = '22222222-2222-2222-2222-222222222222'
const INBOX_ID = '11111111-1111-1111-1111-111111111111'

const maskedEvent = {
  id: EVENT_ID,
  inbox_id: INBOX_ID,
  method: 'POST',
  path: '/hooks/abc',
  query: '',
  content_type: 'application/json',
  body_base64: encodeBody('{"a":1}'),
  body_size: 7,
  client_ip: '127.0.0.1',
  created_at: new Date().toISOString(),
  signature_valid: null,
  headers: [
    { name: 'Content-Type', value: 'application/json', sensitive: false },
    { name: 'Authorization', value: '***redacted***', sensitive: true },
  ],
} as unknown as EventDetail

const revealedEvent = {
  ...maskedEvent,
  headers: [
    { name: 'Content-Type', value: 'application/json', sensitive: false },
    { name: 'Authorization', value: 'Bearer top-secret', sensitive: true },
  ],
} as unknown as EventDetail

function jsonResponse(data: unknown) {
  return {
    ok: true,
    status: 200,
    statusText: 'OK',
    headers: new Headers({ 'content-type': 'application/json' }),
    json: async () => data,
    text: async () => JSON.stringify(data),
  } as unknown as Response
}

function renderEvent() {
  return render(
    <MantineProvider>
      <MemoryRouter initialEntries={[`/inboxes/${INBOX_ID}/events/${EVENT_ID}`]}>
        <Routes>
          <Route path="/inboxes/:id/events/:eid" element={<EventScreen />} />
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  )
}

describe('EventScreen sensitive headers', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/replays')) {
          return jsonResponse({ items: [], total: 0, limit: 20, offset: 0 })
        }

        // The API only hands out clear text when it is explicitly asked to.
        if (url.includes('reveal=true')) {
          return jsonResponse(revealedEvent)
        }

        return jsonResponse(maskedEvent)
      }),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('shows masked values by default and reveals them on request', async () => {
    renderEvent()

    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('tab', { name: /请求头/ }))

    // Masked: the secret itself must never be in the DOM.
    expect(screen.getByText('***redacted***')).toBeInTheDocument()
    expect(screen.queryByText('Bearer top-secret')).not.toBeInTheDocument()

    const toggle = screen.getByRole('switch')

    // Mantine hides the real input behind the track, and user-event refuses to click an
    // element with pointer-events: none - fireEvent.click talks to the input directly.
    fireEvent.click(toggle)

    // Revealed, and the user is told that this access is recorded.
    await waitFor(() => expect(screen.getByText('Bearer top-secret')).toBeInTheDocument())
    expect(screen.getByText(/审计日志/)).toBeInTheDocument()
  })

  it('disables the reveal switch when nothing is protected', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        if (String(input).includes('/replays')) {
          return jsonResponse({ items: [], total: 0, limit: 20, offset: 0 })
        }

        return jsonResponse({
          ...maskedEvent,
          headers: [{ name: 'Content-Type', value: 'application/json', sensitive: false }],
        })
      }),
    )

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('tab', { name: /请求头/ }))

    // The control stays visible (so the UI does not jump) but it cannot be used.
    expect(screen.getByRole('switch')).toBeDisabled()
    expect(screen.queryByText('***redacted***')).not.toBeInTheDocument()
  })

  it('paints the signature verdict with a step that is actually readable', async () => {
    // Valid vs invalid HMAC is a security verdict - if the chip cannot be read the
    // protection has no feedback at all.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/replays')) {
          return jsonResponse({ items: [], total: 0, limit: 20, offset: 0 })
        }

        return jsonResponse({ ...maskedEvent, signature_valid: true })
      }),
    )

    renderEvent()

    await waitFor(() => expect(screen.getByText('签名有效')).toBeInTheDocument())

    const style = screen.getByText('签名有效').closest('.mantine-Badge-root')?.getAttribute('style') ?? ''

    expectReadableBadge(style, 'success.8', 'light')
  })

  it('says why the reveal switch is off when the server has no access control', async () => {
    // Second reason the switch cannot be used, and the one a reviewer cannot guess:
    // without --auth-token/--auth-keys the API is open, so decrypting would be pointless.
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/settings')) {
          return jsonResponse({ auth_enabled: false, encryption_enabled: false } as ServerSettings)
        }

        if (url.includes('/replays')) {
          return jsonResponse({ items: [], total: 0, limit: 20, offset: 0 })
        }

        return jsonResponse(maskedEvent)
      }),
    )

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('tab', { name: /请求头/ }))

    expect(screen.getByRole('switch')).toBeDisabled()
    expect(screen.getByText(/服务端未启用访问控制/)).toBeInTheDocument()
  })
})

function jsonError(
  status: number,
  code: string,
  message: string,
  headers?: Record<string, string>,
): Response {
  return {
    ok: false,
    status,
    statusText: 'Error',
    headers: new Headers({ 'content-type': 'application/json', ...(headers ?? {}) }),
    json: async () => ({ error: { code, message } }),
    text: async () => JSON.stringify({ error: { code, message } }),
  } as unknown as Response
}

/** Fills the target and presses 重放, i.e. exactly what a user does before a 429. */
async function sendReplay() {
  await userEvent.type(screen.getByLabelText('重放目标地址'), 'http://example.com/hook')
  fireEvent.click(screen.getByRole('button', { name: '重放' }))
}

describe('EventScreen replay failures', () => {
  const emptyReplays = { items: [], total: 0, limit: 20, offset: 0 }

  function stubFetch(replayResponse: Response) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/replays')) {
          return jsonResponse(emptyReplays)
        }

        if (url.includes('/replay')) {
          return replayResponse
        }

        return jsonResponse(maskedEvent)
      }),
    )
  }

  afterEach(() => vi.unstubAllGlobals())

  it('keeps the captured request visible when a replay is rate limited', async () => {
    stubFetch(jsonError(429, 'rate_limited', 'too many replays, retry in 30s', { 'Retry-After': '30' }))

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await sendReplay()

    await waitFor(() => expect(screen.getByText(/本调用方触发了重放限流/)).toBeInTheDocument())

    // The wait time comes from Retry-After, so the user knows what to do next.
    expect(screen.getByRole('button', { name: /30 秒后可重试/ })).toBeDisabled()

    // P0: a failed replay must NOT take the event down with it.
    expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument()
    // Pretty printed JSON has a space after the colon, hence the tolerant pattern.
    expect(document.body.textContent).toMatch(/"a":\s*1/)
    expect(screen.queryByText('加载失败')).not.toBeInTheDocument()

    // Server wording is translated away.
    expect(document.body.textContent).not.toMatch(/too many replays/)
  })

  it('counts the cooldown down and re-enables the button', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })

    stubFetch(jsonError(429, 'rate_limited', 'too many replays, retry in 30s', { 'Retry-After': '30' }))

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await sendReplay()
    await waitFor(() => expect(screen.getByRole('button', { name: /30 秒后可重试/ })).toBeInTheDocument())

    await act(async () => {
      vi.advanceTimersByTime(1000)
    })

    expect(screen.getByRole('button', { name: /29 秒后可重试/ })).toBeInTheDocument()

    await act(async () => {
      vi.advanceTimersByTime(30_000)
    })

    expect(screen.getByRole('button', { name: '重新重放' })).toBeEnabled()

    vi.useRealTimers()
  })

  it('derives the wait time from the message when the header is missing', async () => {
    stubFetch(jsonError(429, 'rate_limited', 'too many replays, retry in 12s'))

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await sendReplay()

    await waitFor(() => expect(screen.getByRole('button', { name: /12 秒后可重试/ })).toBeInTheDocument())
  })

  it('reports a blocked target through the result card only', async () => {
    // A blocked target IS recorded server side, so the truthful UI shows the recorded
    // attempt and no separate error card - two cards would contradict each other.
    const blockedAttempt = {
      items: [
        {
          id: '33333333-3333-3333-3333-333333333333',
          outcome: 'blocked',
          target_url: 'http://10.0.0.1/hook',
          duration_ms: 0,
          error: '10.0.0.1 is a reserved address',
          started_at: new Date().toISOString(),
          attempt_no: 1,
        },
      ],
      total: 1,
      limit: 20,
      offset: 0,
    }

    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/replays')) {
          return jsonResponse(blockedAttempt)
        }

        if (url.includes('/replay')) {
          return jsonError(403, 'target_blocked', 'target address is not allowed')
        }

        return jsonResponse(maskedEvent)
      }),
    )

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await sendReplay()

    await waitFor(() => expect(screen.getByText(/目标被安全策略拒绝/)).toBeInTheDocument())

    // The page survives, and there is exactly one verdict - not a second "error" card.
    expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument()
    expect(screen.queryByText('加载失败')).not.toBeInTheDocument()
    expect(screen.queryByText('没有发起重放')).not.toBeInTheDocument()

    // The raw error stays copyable for debugging, but the user also gets it in Chinese -
    // and the remedy names this very address, because "use a public address" would be
    // wrong: they are replaying at their own machine.
    // The same sentence appears twice on purpose: as the hint before sending and as the
    // verdict after. If the two ever drift apart, this count falls back to one.
    expect(screen.getAllByText(/--replay-allow-host 10\.0\.0\.1/).length).toBeGreaterThanOrEqual(2)
    expect(screen.queryByText(/请改用 http\(s\) 公网地址/)).not.toBeInTheDocument()
  })
})

describe('EventScreen replay target reuse', () => {
  const attempt = (target_url: string, outcome: string, started_at: string) => ({
    id: target_url,
    event_id: EVENT_ID,
    attempt_no: 1,
    target_url,
    sign_applied: false,
    started_at,
    duration_ms: 12,
    outcome,
    edited: false,
  })

  /** The server's history plus its settings: both are facts, neither is guessed. */
  function stub(settings: Partial<ServerSettings>, replays: unknown[] = [], calls: string[] = []) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
        calls.push(`${init?.method ?? 'GET'} ${String(input)}`)

        const url = String(input)

        if (url.includes('/settings')) {
          return jsonResponse(settings as ServerSettings)
        }

        if (url.includes('/replays')) {
          return jsonResponse({ items: replays, total: replays.length, limit: 20, offset: 0 })
        }

        return jsonResponse(maskedEvent)
      }),
    )
  }

  beforeEach(() => window.localStorage.clear())
  afterEach(() => vi.unstubAllGlobals())

  it('offers the addresses this event was already replayed at', async () => {
    // The everyday workflow: replay the same event at the same local service, again and
    // again. Typing the URL every time is the friction this removes.
    stub({}, [attempt('http://127.0.0.1:3000/webhook', 'success', '2026-09-13T12:00:00Z')])

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    // Scoped to the chip: the replay history card shows the same URL as plain text.
    const chip = screen.getByRole('checkbox', { name: /127\.0\.0\.1:3000\/webhook/ })

    expect(chip).toBeInTheDocument()
    // The last result travels with the address, so "did it work?" is answered up front.
    expect(chip.closest('.mantine-Chip-root')?.textContent).toContain('上次结果 成功')
  })

  it('fills the box on click and sends nothing', async () => {
    const calls: string[] = []

    stub({}, [attempt('http://127.0.0.1:3000/webhook', 'success', '2026-09-13T12:00:00Z')], calls)

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await userEvent.click(screen.getByRole('checkbox', { name: /127\.0\.0\.1:3000\/webhook/ }))

    // Choosing an address must never fire the request: the user still presses 重放.
    expect(screen.getByLabelText('重放目标地址')).toHaveValue('http://127.0.0.1:3000/webhook')
    expect(calls.filter((c) => c.includes('/replays') && c.startsWith('POST'))).toEqual([])
  })
})

describe('EventScreen blocked address warning', () => {
  function stub(settings: Partial<ServerSettings>) {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/settings')) {
          return jsonResponse(settings as ServerSettings)
        }

        if (url.includes('/replays')) {
          return jsonResponse({ items: [], total: 0, limit: 20, offset: 0 })
        }

        return jsonResponse(maskedEvent)
      }),
    )
  }

  afterEach(() => vi.unstubAllGlobals())

  it('explains the refusal before sending, and still lets the user send', async () => {
    // A reserved address on a plain instance: refused, and the user needs the flags, not
    // a shrug. But we do not decide for them - the button stays live.
    stub({ replay_allow_hosts: [], replay_allow_private: false })

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await userEvent.type(screen.getByLabelText('重放目标地址'), 'http://127.0.0.1:3000/webhook')

    expect(screen.getByText(/当前实例会拒绝它/)).toBeInTheDocument()

    // Twice on purpose: once as the plain-language reason, once as the command to copy.
    expect(screen.getAllByText(/--replay-allow-host 127\.0\.0\.1 --replay-allow-private/).length).toBeGreaterThanOrEqual(2)
    expect(screen.getByRole('button', { name: /重放$/ })).toBeEnabled()
  })

  it('says so when this instance does allow the address', async () => {
    // Same address, different instance: the answer comes from the server's settings, not
    // from a rule baked into the client.
    stub({ replay_allow_hosts: ['127.0.0.1'], replay_allow_private: true })

    renderEvent()
    await waitFor(() => expect(screen.getByText(/\/hooks\/abc/)).toBeInTheDocument())

    await userEvent.type(screen.getByLabelText('重放目标地址'), 'http://127.0.0.1:3000/webhook')

    expect(screen.getByText(/已被当前实例放行/)).toBeInTheDocument()
  })
})
