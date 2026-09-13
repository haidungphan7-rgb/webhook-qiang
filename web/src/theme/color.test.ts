import { describe, expect, it } from 'vitest'
import { methodToColor } from './color'

/**
 * The method colours are the one place where a shade can silently stop doing anything:
 * Mantine caps the text colour of a `light` badge at step 6, so a "fix" that only bumps
 * the step looks changed in the diff and changes nothing on screen. These assertions pin
 * the mapping (MethodBadge paints the text separately - see that component).
 */
describe('methodToColor', () => {
  it('maps every method to a palette step', () => {
    const methods = ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'HEAD', 'OPTIONS', 'TRACE', 'CONNECT']

    for (const method of methods) {
      expect(methodToColor(method)).toMatch(/^[a-z]+\.8$/)
    }
  })

  it('keeps the hues that are read without thinking', () => {
    expect(methodToColor('GET')).toBe('success.8')
    expect(methodToColor('POST')).toBe('amber.8')
    expect(methodToColor('PUT')).toBe('brand.8')
    expect(methodToColor('PATCH')).toBe('violet.8')
    expect(methodToColor('DELETE')).toBe('error.8')
  })

  it('is case insensitive and tolerant of whitespace', () => {
    expect(methodToColor(' post ')).toBe('amber.8')
  })

  it('falls back to gray for anything unknown', () => {
    expect(methodToColor('FROBNICATE')).toBe('gray.8')
    expect(methodToColor('')).toBe('gray.8')
  })
})
