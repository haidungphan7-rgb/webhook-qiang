import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { EventListItem, Inbox } from '~/api/v1'
import { dayjs } from '~/shared/dayjs'
import { expectReadableBadge } from '~/test-utils/contrast'
import { InboxScreen } from './screen'

const INBOX_ID = '11111111-1111-1111-1111-111111111111'

const inbox = (over: Partial<Inbox> = {}): Inbox =>
  ({
    id: INBOX_ID,
    name: 'GitHub 推送',
    token: 'ab12cd34ef56gh78ij90kl12mn34op56qr78',
    enabled: true,
    receive_url: `http://localhost:8080/hooks/ab12cd34ef56gh78ij90kl12mn34op56qr78`,
    event_count: 0,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    response_code: 200,
    response_delay_ms: 0,
    signature_header: 'X-Signature',
    signature_scheme: 'hmac-sha256-hex',
    require_signature: false,
    has_signing_secret: false,
    retention_max_events: 500,
    retention_max_days: 30,
    ...over,
  }) as Inbox

const event = (over: Partial<EventListItem> = {}): EventListItem =>
  ({
    id: '22222222-2222-2222-2222-222222222222',
    inbox_id: INBOX_ID,
    method: 'POST',
    path: '/hooks/abc',
    query: '',
    content_type: 'application/json',
    body_size: 7,
    preview: '{"a":1}',
    client_ip: '127.0.0.1',
    signature_valid: null,
    created_at: new Date().toISOString(),
    replay_count: 0,
    ...over,
  }) as EventListItem

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

const state = {
  inbox: inbox(),
  events: [] as EventListItem[],
  failList: false,
}

/**
 * The screen opens a websocket for live events; jsdom has nothing to connect to, so this
 * one reports "open" (asynchronously, because the handler is attached after construction)
 * to put the screen in the state a user normally sees.
 */
class SilentWebSocket {
  onopen: (() => void) | null = null
  onclose: (() => void) | null = null
  onerror: (() => void) | null = null
  onmessage: (() => void) | null = null

  constructor(public url: string) {
    setTimeout(() => this.onopen?.(), 0)
  }

  close() {}
}

function renderScreen() {
  return render(
    <MantineProvider>
      <MemoryRouter initialEntries={[`/inboxes/${INBOX_ID}`]}>
        <Routes>
          <Route path="/inboxes/:id" element={<InboxScreen />} />
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  )
}

describe('InboxScreen empty and error states', () => {
  beforeEach(() => {
    state.inbox = inbox()
    state.events = []
    state.failList = false

    vi.stubGlobal('WebSocket', SilentWebSocket)
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/events')) {
          if (state.failList) {
            // What a dead server (or a blocked request) actually looks like.
            throw new Error('Failed to fetch')
          }

          return jsonResponse({ items: state.events, total: state.events.length, limit: 20, offset: 0 })
        }

        return jsonResponse(state.inbox)
      }),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  it('waits for the first event and offers a ready made curl', async () => {
    renderScreen()

    await waitFor(() => expect(screen.getByText('正在监听这个地址')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '复制一条 cURL 试试' })).toBeInTheDocument()

    // The waiting hint shows the address masked, so it can be recognised at a glance.
    expect(screen.getByText(/••••/)).toBeInTheDocument()
  })

  it('tells "no matches" apart from "nothing yet" once a filter is active', async () => {
    renderScreen()
    await waitFor(() => expect(screen.getByText('正在监听这个地址')).toBeInTheDocument())

    await userEvent.type(screen.getByPlaceholderText(/搜索请求体关键字/), 'zzz')

    await waitFor(() => expect(screen.getByText('没有匹配的事件')).toBeInTheDocument())
    expect(screen.getByRole('button', { name: '清除筛选' })).toBeInTheDocument()
  })

  it('explains a disabled inbox instead of asking the user to wait', async () => {
    state.inbox = inbox({ enabled: false })

    renderScreen()

    await waitFor(() => expect(screen.getByText('这个收件箱已停用')).toBeInTheDocument())
    expect(screen.queryByText('正在监听这个地址')).not.toBeInTheDocument()
  })

  it('shows the error state - and recovers when retry succeeds', async () => {
    state.failList = true

    renderScreen()

    await waitFor(() => expect(screen.getByText('加载失败')).toBeInTheDocument())
    // Server wording is translated away; nothing English reaches the user.
    expect(screen.getByText('操作失败，请重试')).toBeInTheDocument()

    state.failList = false

    await userEvent.click(screen.getByRole('button', { name: '重试' }))

    await waitFor(() => expect(screen.getByText('正在监听这个地址')).toBeInTheDocument())
    expect(screen.queryByText('加载失败')).not.toBeInTheDocument()
  })

  it('lists events with their method, size and replay state', async () => {
    state.events = [event({ method: 'POST', preview: '{"a":1}', body_size: 7 })]

    renderScreen()

    await waitFor(() => expect(screen.getByText('{"a":1}')).toBeInTheDocument())
    expect(screen.getByText('7 B')).toBeInTheDocument()
    // Scoped to the table: the method filter is a Select, and a collapsed Select still
    // keeps every option ("POST" among them) in the DOM.
    expect(within(screen.getByRole('table')).getByText('POST')).toBeInTheDocument()
  })

  it('paints the live connection badge with a step that is actually readable', async () => {
    renderScreen()

    await waitFor(() => expect(screen.getByText(/● 实时/)).toBeInTheDocument())

    // Whether the live stream is up is the one thing on this page that changes without a
    // reload; at 2.17:1 it was there but unreadable.
    const style = screen.getByText(/● 实时/).closest('.mantine-Badge-root')?.getAttribute('style') ?? ''

    expectReadableBadge(style, 'success.8', 'light')
  })

  it('shows the absolute time, not a relative one', async () => {
    // "3 分钟前" cannot be matched against a third party's log, and a relative time is
    // invisible to a screen reader that only reads the text of the cell.
    state.events = [event()]

    renderScreen()

    const stamp = dayjs(state.events[0].created_at).format('MM-DD HH:mm:ss')

    await waitFor(() => expect(screen.getByText(stamp)).toBeInTheDocument())
    expect(screen.queryByText(/前$/)).not.toBeInTheDocument()
  })
})
