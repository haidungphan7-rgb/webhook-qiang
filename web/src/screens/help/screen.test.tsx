import { MantineProvider } from '@mantine/core'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { Link, MemoryRouter, Route, Routes } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { ServerSettings } from '~/api/v1'
import { HelpScreen } from './screen'

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

function renderHelp(initial = '/help') {
  return render(
    <MantineProvider>
      <MemoryRouter initialEntries={[initial]}>
        <Routes>
          <Route path="/help" element={<HelpScreen />} />
        </Routes>
      </MemoryRouter>
    </MantineProvider>,
  )
}

/** Only the fields a given case cares about; cast through `unknown` - tsc rejects a bare `as` on a partial object. */
function stubSettings(fields: Record<string, unknown>) {
  vi.stubGlobal('fetch', vi.fn(async () => jsonResponse(fields as unknown as ServerSettings)))
}

describe('HelpScreen deep links', () => {
  beforeEach(() => {
    stubSettings({ replay_allow_hosts: [], replay_allow_private: false })
  })

  afterEach(() => vi.unstubAllGlobals())

  it('opens the FAQ entry the link points at', async () => {
    // A link into a collapsed accordion is a link to nothing: the reader arrives at the
    // question they were sent for and still has to find it themselves.
    renderHelp('/help#blocked')

    const control = await screen.findByRole('button', { name: /被拦截/ })

    await waitFor(() => expect(control).toHaveAttribute('aria-expanded', 'true'))
  })

  it('scrolls the target into view instead of leaving it below the fold', async () => {
    const scroll = vi.spyOn(window.HTMLElement.prototype, 'scrollIntoView')

    renderHelp('/help#blocked')

    await waitFor(() => expect(scroll).toHaveBeenCalled())
  })

  it('leaves the FAQ closed when nothing was asked for', async () => {
    renderHelp()

    const control = await screen.findByRole('button', { name: /被拦截/ })

    expect(control).toHaveAttribute('aria-expanded', 'false')
  })

  it('follows a deep link that is clicked while the guide is already open', async () => {
    // A hash-only navigation does not remount the screen, which is exactly how the event
    // page sends people here from a refused replay.
    render(
      <MantineProvider>
        <MemoryRouter initialEntries={['/help']}>
          <Routes>
            <Route
              path="/help"
              element={
                <>
                  <Link to="/help#blocked">去看被拦截</Link>
                  <HelpScreen />
                </>
              }
            />
          </Routes>
        </MemoryRouter>
      </MantineProvider>,
    )

    const control = await screen.findByRole('button', { name: /被拦截/ })
    expect(control).toHaveAttribute('aria-expanded', 'false')

    await userEvent.click(screen.getByText('去看被拦截'))

    await waitFor(() => expect(control).toHaveAttribute('aria-expanded', 'true'))
  })

  it('gives every linkable target an id', async () => {
    // The event screen links to both: the rules block and the "why was I blocked" answer.
    renderHelp()

    await screen.findByText('常见问题')

    expect(document.getElementById('replay')).not.toBeNull()
    expect(document.getElementById('blocked')).not.toBeNull()
  })
})

describe('HelpScreen version footer', () => {
  afterEach(() => vi.unstubAllGlobals())

  it('shows the version the server reports, so nobody has to open a shell', async () => {
    // "Which version are you on?" is the first question in every bug report.
    stubSettings({ version: '1.4.0', build_time: '2026-09-13T10:20:00Z' })

    renderHelp()

    expect(await screen.findByText(/当前版本：1\.4\.0/)).toBeInTheDocument()
  })

  it('says unknown instead of printing the go run placeholder as a version', async () => {
    // `0.0.0@undefined` / `unknown` mean "this build was never stamped", not "version
    // 0.0.0". Printing it as a version would be a made up answer.
    stubSettings({ version: '0.0.0@undefined', build_time: 'unknown' })

    renderHelp()

    expect(await screen.findByText(/版本未知/)).toBeInTheDocument()
    expect(screen.queryByText(/当前版本：/)).not.toBeInTheDocument()
  })

  it('does not invent a build time it cannot read', async () => {
    stubSettings({ version: '1.4.0', build_time: 'unknown' })

    renderHelp()

    expect(await screen.findByText('当前版本：1.4.0')).toBeInTheDocument()
    expect(screen.queryByText(/构建于/)).not.toBeInTheDocument()
  })
})
