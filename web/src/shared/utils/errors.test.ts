import { describe, expect, it } from 'vitest'
import { ApiError } from '~/api/v1'
import { humanize } from './errors'

describe('humanize', () => {
  it('maps known codes to Chinese', () => {
    expect(humanize(new ApiError(429, 'rate_limited', 'too many replays, retry in 30s'))).toBe(
      '重放太频繁了，请稍后再试',
    )

    expect(humanize(new ApiError(403, 'target_blocked', 'target address is not allowed'))).toContain(
      '安全策略拒绝',
    )

    expect(humanize(new ApiError(413, 'body_too_large', 'request body too large'))).toContain('大小上限')
    expect(humanize(new ApiError(422, 'validation_error', 'name is required'))).toBe('填写的内容不符合要求')
  })

  it('falls back to the status when the code is unknown', () => {
    expect(humanize(new ApiError(429, 'brand_new_code', 'Too Many Requests'))).toBe('操作太频繁了，请稍后再试')
    expect(humanize(new ApiError(500, 'brand_new_code', 'boom'))).toBe('服务出错了，请查看服务日志')
    expect(humanize(new ApiError(404, 'brand_new_code', 'nope'))).toContain('找不到')
  })

  it('never leaks the server sentence', () => {
    const cases = [
      new ApiError(429, 'rate_limited', 'too many replays, retry in 30s'),
      new ApiError(403, 'target_blocked', '10.0.0.1 is a reserved address'),
      new ApiError(500, 'brand_new_code', 'internal panic: nil map'),
      new ApiError(422, 'validation_error', 'body_base64 must be base64 encoded'),
    ]

    for (const err of cases) {
      expect(humanize(err)).not.toMatch(
        /too many|reserved address|panic|base64|Too Many Requests/i,
      )
    }
  })

  it('passes plain errors through, but not network noise', () => {
    expect(humanize(new Error('磁盘已满'))).toBe('磁盘已满')
    expect(humanize(new Error('Failed to fetch'))).toBe('操作失败，请重试')
    expect(humanize(new Error('fetch failed'))).toBe('操作失败，请重试')
  })

  it('handles values that are not errors at all', () => {
    for (const value of [null, undefined, 'boom', 42, {}]) {
      expect(humanize(value)).toBe('操作失败，请重试')
    }
  })

  it('has a last resort for everything it cannot classify', () => {
    // 502 is not in the status table and the code is unknown: say "retry", never the
    // English server sentence.
    expect(humanize(new ApiError(502, 'brand_new_code', 'bad gateway'))).toBe('操作失败，请重试')
    expect(humanize(new ApiError(599, 'brand_new_code', 'weird'))).toBe('操作失败，请重试')

    // An Error with an empty message must not render as an empty box.
    expect(humanize(new Error(''))).toBe('操作失败，请重试')
    expect(humanize(new Error('   '))).toBe('操作失败，请重试')
  })

  it('has a mapping for every code the API can return', () => {
    // Guards against "added a new error code in Go, forgot the Chinese sentence".
    const codes = [
      'rate_limited',
      'target_blocked',
      'invalid_target',
      'validation_error',
      'invalid_id',
      'invalid_json',
      'unauthorized',
      'not_found',
      'inbox_disabled',
      'body_too_large',
      'encryption_required',
    ]

    for (const code of codes) {
      const text = humanize(new ApiError(400, code, 'server sentence'))

      expect(text.length).toBeGreaterThan(0)
      expect(text).not.toMatch(/server sentence/)
      // No English error wording; technical terms like "http(s)" are fine.
      expect(text).not.toMatch(/\b(denied|invalid|required|reserved|failed|error|unauthorized)\b/i)
    }
  })
})
