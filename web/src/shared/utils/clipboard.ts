/**
 * Copy text to the clipboard, with a fallback.
 *
 * `navigator.clipboard` only exists in a secure context: over plain http it is undefined
 * on anything that is not localhost (i.e. exactly how this app is often demoed, via a LAN
 * IP). Mantine's own useClipboard hook has no fallback and swallows the failure, so every
 * "copy" button would silently do nothing. That is why this exists.
 */
export async function copyText(value: string): Promise<boolean> {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value)

      return true
    }
  } catch {
    // Permission denied or a non-secure context: fall through to the legacy path.
  }

  return legacyCopy(value)
}

function legacyCopy(value: string): boolean {
  const area = document.createElement('textarea')

  area.value = value
  area.setAttribute('readonly', '')
  area.style.position = 'fixed'
  area.style.top = '-1000px'
  area.style.opacity = '0'

  document.body.appendChild(area)
  area.select()

  let ok = false

  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }

  document.body.removeChild(area)

  return ok
}
