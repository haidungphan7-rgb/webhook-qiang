import { MantineProvider } from '@mantine/core'
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { copyText } from '~/shared/utils/clipboard'
import { CopyIconButton, CopyTextButton } from './copy-button'

// The clipboard itself is covered in clipboard.test.ts. What matters here is that a
// FAILED copy is visible to the user, so stub it and drive both outcomes directly.
vi.mock('~/shared/utils/clipboard', () => ({ copyText: vi.fn() }))

const copyTextMock = vi.mocked(copyText)

function renderButton(ui: React.ReactElement) {
  return render(<MantineProvider>{ui}</MantineProvider>)
}

beforeEach(() => {
  copyTextMock.mockResolvedValue(true)
})

afterEach(() => {
  vi.clearAllMocks()
})

describe('CopyTextButton', () => {
  it('confirms a successful copy in the button label', async () => {
    renderButton(<CopyTextButton value="curl -X POST https://example.com" />)

    await userEvent.click(screen.getByRole('button', { name: '复制' }))

    expect(copyTextMock).toHaveBeenCalledWith('curl -X POST https://example.com')
    await waitFor(() => expect(screen.getByRole('button', { name: '已复制' })).toBeInTheDocument())
  })

  it('opens a manual copy dialog instead of failing silently', async () => {
    // The situation this exists for: http://<LAN IP> has no navigator.clipboard at all.
    copyTextMock.mockResolvedValue(false)

    renderButton(<CopyTextButton value="Bearer top-secret" />)
    await userEvent.click(screen.getByRole('button', { name: '复制' }))

    await waitFor(() => expect(screen.getByText('请手动复制')).toBeInTheDocument())
    expect(screen.getByRole('textbox')).toHaveValue('Bearer top-secret')
    expect(screen.getByText(/浏览器只在 https 或 localhost 下开放剪贴板/)).toBeInTheDocument()
  })

  it('returns to the idle label so the button can be used twice', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true })

    renderButton(<CopyTextButton value="x" />)
    await userEvent.click(screen.getByRole('button', { name: '复制' }))
    await waitFor(() => expect(screen.getByRole('button', { name: '已复制' })).toBeInTheDocument())

    await act(async () => {
      vi.advanceTimersByTime(1600)
    })

    expect(screen.getByRole('button', { name: '复制' })).toBeInTheDocument()

    vi.useRealTimers()
  })
})

describe('CopyIconButton', () => {
  it('carries an accessible name - an icon alone identifies nothing', () => {
    renderButton(<CopyIconButton value="x" label="复制为 cURL" />)

    expect(screen.getByRole('button', { name: '复制为 cURL' })).toBeInTheDocument()
  })
})
