import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { Inbox, ServerSettings } from '~/api/v1'
import Layout from './layout'

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

const inbox = (id: string, name: string): Inbox =>
  ({ id, name, enabled: true, event_count: 3, receive_url: `http://localhost:8080/hooks/${id}` }) as Inbox

const INBOXES = [
  inbox('11111111-1111-1111-1111-111111111111', 'GitHub 推送'),
  inbox('33333333-3333-3333-3333-333333333333', 'Stripe 回调'),
]

const SETTINGS = { auth_enabled: false, replay_timeout_ms: 10000 } as ServerSettings

function renderLayout() {
  return render(
    <MantineProvider>
      <MemoryRouter initialEntries={['/']}>
        <Routes>
          <Route path="/" element={<Layout />}>
            <Route index element={<div>收件箱列表页</div>} />
            <Route path="inboxes/:id" element={<div>某个收件箱详情</div>} />
          </Route>
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  )
}

describe('Layout command palette', () => {
  beforeEach(() => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async (input: RequestInfo | URL) => {
        const url = String(input)

        if (url.includes('/settings')) {
          return jsonResponse(SETTINGS)
        }

        if (url.includes('/inboxes')) {
          return jsonResponse({ items: INBOXES, total: INBOXES.length, limit: 50, offset: 0 })
        }

        return jsonResponse({})
      }),
    )
  })

  afterEach(() => vi.unstubAllGlobals())

  const openPalette = async () => {
    renderLayout()

    await userEvent.keyboard('{Control>}k{/Control}')

    return screen.findByText('跳转到收件箱')
  }

  it('opens on mod+K and lists the inboxes', async () => {
    await openPalette()

    await waitFor(() => expect(screen.getByText('GitHub 推送')).toBeInTheDocument())
    expect(screen.getByText('Stripe 回调')).toBeInTheDocument()
  })

  it('filters by name as you type', async () => {
    await openPalette()

    await userEvent.type(await screen.findByLabelText('输入名称过滤'), 'stripe')

    await waitFor(() => expect(screen.queryByText('GitHub 推送')).not.toBeInTheDocument())
    expect(screen.getByText('Stripe 回调')).toBeInTheDocument()
  })

  it('says so when nothing matches', async () => {
    await openPalette()

    await userEvent.type(await screen.findByLabelText('输入名称过滤'), 'zzz')

    await waitFor(() => expect(screen.getByText('没有匹配的收件箱。')).toBeInTheDocument())
  })

  it('jumps to the first match on Enter', async () => {
    await openPalette()

    const input = await screen.findByLabelText('输入名称过滤')

    await userEvent.type(input, 'stripe')
    await userEvent.keyboard('{Enter}')

    // The palette closes and the router moved - one key press, no mouse.
    await waitFor(() => expect(screen.getByText('某个收件箱详情')).toBeInTheDocument())
  })

  it('jumps when a row is clicked', async () => {
    await openPalette()

    await waitFor(() => expect(screen.getByText('GitHub 推送')).toBeInTheDocument())

    await userEvent.click(screen.getByText('GitHub 推送'))

    await waitFor(() => expect(screen.getByText('某个收件箱详情')).toBeInTheDocument())
  })
})
