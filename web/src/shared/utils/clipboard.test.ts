import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyText } from './clipboard'

function stubClipboard(impl?: () => Promise<void>) {
  const writeText = vi.fn(impl ?? (() => Promise.resolve()))

  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  })

  return writeText
}

function stubExecCommand(result: boolean | (() => boolean) | (() => never)) {
  const fn = vi.fn(typeof result === 'boolean' ? () => result : result)

  Object.defineProperty(document, 'execCommand', { configurable: true, value: fn })

  return fn
}

afterEach(() => {
  vi.restoreAllMocks()
  Reflect.deleteProperty(navigator, 'clipboard')
  Reflect.deleteProperty(document, 'execCommand')
})

describe('copyText', () => {
  it('uses the async clipboard when it is available', async () => {
    const writeText = stubClipboard()
    const exec = stubExecCommand(true)

    await expect(copyText('hello')).resolves.toBe(true)
    expect(writeText).toHaveBeenCalledWith('hello')
    expect(exec).not.toHaveBeenCalled()
  })

  it('falls back to execCommand when the clipboard API rejects', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')))
    const exec = stubExecCommand(true)

    await expect(copyText('hello')).resolves.toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
  })

  it('falls back when navigator.clipboard does not exist at all', async () => {
    // This is the real situation over plain http on a LAN address.
    Reflect.deleteProperty(navigator, 'clipboard')
    const exec = stubExecCommand(true)

    await expect(copyText('hello')).resolves.toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
  })

  it('reports false when every path fails', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')))
    stubExecCommand(false)

    await expect(copyText('hello')).resolves.toBe(false)
  })

  it('does not throw when execCommand itself throws', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')))
    stubExecCommand(() => {
      throw new Error('unsupported')
    })

    await expect(copyText('hello')).resolves.toBe(false)
  })

  it('puts the text in the textarea before asking the browser to copy it', async () => {
    // The only way to prove the legacy path copies the RIGHT text: peek at the textarea
    // while execCommand runs, because it is removed immediately afterwards.
    let seen: string | undefined

    stubClipboard(() => Promise.reject(new Error('denied')))
    stubExecCommand(() => {
      seen = (document.querySelector('textarea') as HTMLTextAreaElement | null)?.value

      return true
    })

    await expect(copyText('Bearer top-secret')).resolves.toBe(true)
    expect(seen).toBe('Bearer top-secret')
  })

  it('falls back when navigator.clipboard exists but has no writeText', async () => {
    // navigator.clipboard = {} happens in some embedded webviews; the optional chain
    // must fall through to execCommand.
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: {} })
    const exec = stubExecCommand(true)

    await expect(copyText('hello')).resolves.toBe(true)
    expect(exec).toHaveBeenCalledWith('copy')
  })

  it('leaves no textarea behind', async () => {
    stubClipboard(() => Promise.reject(new Error('denied')))
    stubExecCommand(true)

    await copyText('hello')

    expect(document.querySelectorAll('textarea')).toHaveLength(0)
  })
})
