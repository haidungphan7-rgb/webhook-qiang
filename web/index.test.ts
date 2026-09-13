import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// The language, the title and the pre-mount theme script live in index.html rather than
// in React, so nothing else in the suite can catch it if they regress.
const html = readFileSync(join(process.cwd(), 'index.html'), 'utf8')

describe('index.html', () => {
  it('declares the page language as Chinese', () => {
    expect(html).toMatch(/<html[^>]*\slang="zh-CN"/)
  })

  it('has a Chinese title and description', () => {
    expect(html).toMatch(/<title>[^<]*[一-鿿]/)
    expect(html).toMatch(/<meta name="description" content="[^"]*[一-鿿]/)
  })

  it('applies the stored colour scheme before React mounts', () => {
    // Without this the app flashes white on every load in dark mode.
    expect(html).toMatch(/mantine-color-scheme-value/)
    expect(html).toMatch(/data-mantine-color-scheme/)
  })
})
