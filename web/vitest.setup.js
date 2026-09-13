import '@testing-library/jest-dom/vitest'
import { vi } from 'vitest'

const { getComputedStyle } = window

window.getComputedStyle = (elt) => getComputedStyle(elt)
window.HTMLElement.prototype.scrollIntoView = () => {}

Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: vi.fn().mockImplementation((query) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
})

class ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

window.ResizeObserver = ResizeObserver

// Under vitest the document URL is "about:blank", where jsdom exposes no localStorage at
// all - any component that remembers the last replay target would crash.
if (!window.localStorage) {
  const store = new Map()

  Object.defineProperty(window, 'localStorage', {
    configurable: true,
    value: {
      getItem: (k) => (store.has(k) ? store.get(k) : null),
      setItem: (k, v) => store.set(k, String(v)),
      removeItem: (k) => store.delete(k),
      clear: () => store.clear(),
      key: (i) => Array.from(store.keys())[i] ?? null,
      get length() {
        return store.size
      },
    },
  })
}
