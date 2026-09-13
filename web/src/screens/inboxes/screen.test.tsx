import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Inbox } from '~/api/v1'
import { expectReadableBadge } from '~/test-utils/contrast'
import { InboxesScreen } from './screen'

const inbox = (over: Partial<Inbox> = {}): Inbox =>
  ({
    id: '11111111-1111-1111-1111-111111111111',
    name: 'GitHub 推送',
    token: 'ab12cd34ef56gh78ij90kl12mn34op56qr78',
    enabled: true,
    receive_url: 'http://localhost:8080/hooks/ab12cd34ef56gh78ij90kl12mn34op56qr78',
    event_count: 3,
    last_event_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    response_code: 200,
    response_delay_ms: 0,
    response_headers: [],
    signature_header: 'X-Signature',
    signature_scheme: 'hmac-sha256-hex',
    require_signature: false,
    has_signing_secret: false,
    retention_max_events: 500,
    retention_max_days: 30,
    ...over,
  }) as Inbox

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

function renderScreen() {
  return render(
    <MantineProvider>
      <MemoryRouter>
        <InboxesScreen />
      </MemoryRouter>
    </MantineProvider>,
  )
}

describe('InboxesScreen', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/api/v1/inboxes')) {
          return jsonResponse({ items: (globalThis as never as { __items: Inbox[] }).__items, total: 0, limit: 20, offset: 0 })
        }

        return jsonResponse({})
      }),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('invites the user to create a first inbox when there is none', async () => {
    ;(globalThis as never as { __items: Inbox[] }).__items = []

    renderScreen()

    await waitFor(() => expect(screen.getByText('还没有收件箱')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: /创建第一个收件箱/ })).toBeInTheDocument()
  })

  it('tells "no matches" apart from "nothing yet" once a filter is active', async () => {
    ;(globalThis as never as { __items: Inbox[] }).__items = []

    renderScreen()
    await waitFor(() => expect(screen.getByText('还没有收件箱')).toBeInTheDocument())

    // Typing a keyword switches the empty state to the filtered variant, which offers a
    // way out instead of telling the user to create something that already exists.
    await userEvent.type(screen.getByPlaceholderText(/按名称搜索/), 'zzz')

    await waitFor(() => expect(screen.getByText('没有匹配的收件箱')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '清除筛选' })).toBeInTheDocument()

    // The first run guide is gone: with inboxes present it would just be noise.
    expect(screen.queryByText(/建一个收件箱/)).not.toBeInTheDocument()
  })

  it('explains what an inbox is the first time there is nothing', async () => {
    ;(globalThis as never as { __items: Inbox[] }).__items = []

    renderScreen()

    await waitFor(() => expect(screen.getByText('还没有收件箱')).toBeInTheDocument())

    const steps = screen.getAllByRole('listitem')

    // Pinned, because this is the one piece of copy a first time visitor reads: it has to
    // stay about what a webhook is, not about what we built.
    expect(steps.map((li) => li.textContent ?? '')).toEqual([
      '建一个收件箱：拿到一个专属的接收地址（就是一串 URL），不需要你部署任何东西。',
      '把地址填给第三方：在对方的 webhook 设置里填这串 URL —— 那通常就叫「回调地址 / Webhook URL」。',
      '回来查看并重放：请求会实时出现在这里；确认内容后，可以把同一条原样重放到你的服务，用来复现问题。',
    ])
  })

  it('hides the first run guide as soon as there is an inbox', async () => {
    ;(globalThis as never as { __items: Inbox[] }).__items = [inbox()]

    renderScreen()

    await waitFor(() => expect(screen.getByText('GitHub 推送')).toBeInTheDocument())

    expect(screen.queryByText('还没有收件箱')).not.toBeInTheDocument()
    expect(screen.queryByText(/建一个收件箱/)).not.toBeInTheDocument()
  })

  it('shows the name and a masked receive url for every inbox', async () => {
    ;(globalThis as never as { __items: Inbox[] }).__items = [inbox()]

    renderScreen()

    await waitFor(() => expect(screen.getByText('GitHub 推送')).toBeInTheDocument())

    // The token is readable but not copyable from the screen: it is masked in the middle.
    expect(screen.getByText(/••••/)).toBeInTheDocument()
    expect(screen.queryByText(/ab12cd34ef56gh78ij90kl12mn34op56qr78/)).not.toBeInTheDocument()
  })

  it('paints the enabled/disabled badge with a step that is actually readable', async () => {
    ;(globalThis as never as { __items: Inbox[] }).__items = [inbox()]

    renderScreen()

    // Scoped to the table: the status filter is a SegmentedControl that also has an
    // "启用" option.
    await waitFor(() => expect(within(screen.getByRole('table')).getByText('启用')).toBeInTheDocument())

    // "启用" used to sit at 2.17:1 - a Mantine `light` badge caps the text at step 6, no
    // matter which step you pass. The state was there, it just could not be read.
    const style =
      within(screen.getByRole('table')).getByText('启用').closest('.mantine-Badge-root')?.getAttribute('style') ?? ''

    expectReadableBadge(style, 'success.8', 'light')
  })

  it('shows the response code each inbox will answer with', async () => {
    ;(globalThis as never as { __items: Inbox[] }).__items = [inbox({ response_code: 418 })]

    renderScreen()

    await waitFor(() => expect(screen.getByText('418')).toBeInTheDocument())
  })
})
