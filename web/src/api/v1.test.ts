import { describe, expect, it } from 'vitest'
import { buildQuery, decodeBody, encodeBody, formatBytes, prettyPrint } from './v1'

/**
 * The frontend tests concentrate on the pure helpers that encode the product rules:
 * bodies must survive the base64 round trip untouched, JSON pretty printing must never
 * pretend to succeed, and filter parameters must not be sent when they are empty.
 * They need no DOM, so they run anywhere and never flake.
 */
describe('body transport', () => {
  it('round trips text through base64', () => {
    const text = '{"event":"order.paid","amount":42.5}'

    expect(decodeBody(encodeBody(text))).toBe(text)
  })

  it('keeps multibyte characters intact (bytes, not chars)', () => {
    const text = '{"msg":"你好，世界 🎉"}'

    expect(decodeBody(encodeBody(text))).toBe(text)
  })

  it('returns an empty string for an empty payload', () => {
    expect(decodeBody('')).toBe('')
  })
})

describe('prettyPrint', () => {
  it('formats valid JSON', () => {
    expect(prettyPrint('{"a":1}')).toBe('{\n  "a": 1\n}')
  })

  it('returns null for invalid JSON instead of throwing', () => {
    expect(prettyPrint('not json at all')).toBeNull()
    expect(prettyPrint('')).toBeNull()
    expect(prettyPrint('{"a":')).toBeNull()
  })

  it('does not modify the original text', () => {
    const raw = '{"a":1,"b":[1,2]}'

    prettyPrint(raw)

    expect(raw).toBe('{"a":1,"b":[1,2]}')
  })
})

describe('formatBytes', () => {
  it('renders bytes', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(512)).toBe('512 B')
  })

  it('renders kilobytes and megabytes', () => {
    expect(formatBytes(1024)).toBe('1.0 KB')
    expect(formatBytes(1048576)).toBe('1.00 MB')
  })
})

describe('buildQuery', () => {
  it('is empty when there is nothing to filter', () => {
    expect(buildQuery({})).toBe('')
    expect(buildQuery({ q: '', from: undefined })).toBe('')
  })

  it('serialises the values that are set', () => {
    const qs = buildQuery({ q: 'order', content_type: 'application/json', limit: 20, offset: 0 })

    expect(qs).toContain('q=order')
    expect(qs).toContain('limit=20')
  })

  it('keeps offset=0 (a real value) but drops empty strings', () => {
    const qs = buildQuery({ offset: 0, q: '' })

    expect(qs).toBe('?offset=0')
  })

  it('sends booleans as the literal "true" / "false"', () => {
    // Contract with the Go side: query flags are parsed by truthy(), which accepts
    // 1/true/yes/on - so "true" is valid and must not be "fixed" back to "1" (`reveal`
    // goes through the same helper, and a wrong value silently returns masked headers).
    expect(buildQuery({ reveal: true })).toBe('?reveal=true')
    expect(buildQuery({ reveal: false })).toBe('?reveal=false')
    expect(buildQuery({ enabled: true, q: 'x' })).toBe('?enabled=true&q=x')
  })
})
