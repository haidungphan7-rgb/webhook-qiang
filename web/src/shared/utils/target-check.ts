/**
 * Client side sanity check for a replay target.
 *
 * It exists to answer the question the server used to answer only AFTER the user pressed
 * the button: "will this address be refused?" Getting that answer while typing is the
 * difference between a 30 second success and five minutes of confusion.
 *
 * It deliberately mirrors internal/replay/policy.go but only for literal addresses: a
 * host name cannot be resolved from the browser, so those are left to the server (which
 * re-checks everything anyway - this is a hint, never the enforcement).
 */

export type TargetLevel = 'ok' | 'warn' | 'error'

export type TargetCheck = {
  level: TargetLevel
  message?: string
}

const OK: TargetCheck = { level: 'ok' }

export function checkTarget(
  raw: string,
  allowHosts: string[] = [],
  allowPrivate = false,
): TargetCheck {
  const value = raw.trim()
  if (value === '') {
    return OK
  }

  let url: URL
  try {
    url = new URL(value)
  } catch {
    return { level: 'error', message: '地址不完整：要以 http:// 或 https:// 开头' }
  }

  if (url.protocol !== 'http:' && url.protocol !== 'https:') {
    return { level: 'error', message: `不支持 ${url.protocol.replace(':', '')}，只能用 http 或 https` }
  }

  if (url.username !== '' || url.password !== '') {
    return { level: 'error', message: '地址里不能带用户名和密码' }
  }

  const host = url.hostname.replace(/^\[|\]$/g, '')
  if (host === '') {
    return { level: 'error', message: '缺少主机名' }
  }

  const listed = allowHosts.some((h) => h.trim().toLowerCase() === host.toLowerCase())

  // An allow list is a hard gate server side (policy.go): anything outside it is refused
  // no matter where it resolves to. This must come BEFORE the "is it reserved?" question,
  // otherwise a public host outside the list is silently waved through.
  if (allowHosts.length > 0 && !listed) {
    return {
      level: 'warn',
      message: `这台主机不在服务端的允许名单里（当前允许：${allowHosts.join('、')}）`,
    }
  }

  if (!isReserved(host)) {
    // A name cannot be resolved from the browser, so say who decides instead of
    // implying the check is done.
    if (!isIPLiteral(host)) {
      return { level: 'ok', message: '域名是否解析到内网，要发送后由服务端判断。' }
    }

    return OK
  }

  // Reserved, but the operator may have explicitly allowed this host.
  if (listed && allowPrivate) {
    return OK
  }

  return {
    level: 'warn',
    message: '这是内网 / 本机地址，默认会被服务端拒绝（本实例放行时仍可用）',
  }
}

/** The two server settings that decide whether a target is refused. */
export type PolicySettings = { replay_allow_hosts?: string[]; replay_allow_private?: boolean } | null | undefined

export type TargetVerdictKind = 'invalid' | 'not-allowed' | 'local'

export type TargetVerdict = {
  /**
   * `true`  - the server will refuse this address (it becomes `target_blocked`).
   * `false` - it will go through.
   * `null`  - the browser cannot know: a host name, whose address only the server sees.
   */
  blocked: boolean | null
  kind: TargetVerdictKind | null
  /** One sentence in the user's language, ready to display. Empty when there is nothing to say. */
  reason: string
  /** The exact command that would allow this address, ready to copy. Absent when nothing would help. */
  command?: string
}

/** `webhook-zq` is the released binary name; `start` is the subcommand that runs the server. */
function allowCommand(host: string, allowPrivate: boolean): string {
  return `webhook-zq start --replay-allow-host ${host}${allowPrivate ? ' --replay-allow-private' : ''}`
}

/**
 * What the server will do with this address on THIS instance.
 *
 * Same question as `checkTarget`, answered from the live settings and phrased as a
 * verdict with a remedy, so the panel can say "this will be refused, and here is the flag
 * that changes it" before the user presses anything. The point is to explain, never to
 * block: the replay button stays enabled - a refusal is the server's call, not ours.
 */
export function willBeBlocked(raw: string, settings: PolicySettings = null): TargetVerdict {
  const hosts = (settings?.replay_allow_hosts ?? []).filter((h): h is string => typeof h === 'string')
  const allowPrivate = settings?.replay_allow_private === true
  const check = checkTarget(raw, hosts, allowPrivate)

  if (check.level === 'error') {
    return { blocked: true, kind: 'invalid', reason: check.message ?? '' }
  }

  if (check.level === 'warn') {
    if (isLocalAddress(raw)) {
      // Reserved and not allowed on this instance. Both flags are needed: being on the
      // allow list is not enough for a reserved address, and vice versa.
      const host = hostOf(raw) || '<host>'

      return {
        blocked: true,
        kind: 'local',
        reason: `这是内网 / 保留地址，当前实例会拒绝它（target_blocked）。要重放到这个地址，需要用 --replay-allow-host ${host} --replay-allow-private 启动实例。`,
        command: allowCommand(host, true),
      }
    }

    // Refused for a different reason than "reserved": it is simply not on the list. Say
    // that (and how to add it) instead of calling it an internal address.
    const host = hostOf(raw)

    return {
      blocked: true,
      kind: 'not-allowed',
      reason: `${check.message ?? ''}${host ? `要用它，请以 --replay-allow-host ${host} 启动实例。` : ''}`,
      command: host ? allowCommand(host, false) : undefined,
    }
  }

  // Reserved, and this instance allows it: worth saying out loud, because the same
  // address is refused on most instances and the user may be comparing the two.
  if (isLocalAddress(raw)) {
    return { blocked: false, kind: 'local', reason: '此地址已被当前实例放行。' }
  }

  const host = hostOf(raw)
  if (host !== '' && !isIPLiteral(host) && !host.includes(':')) {
    return { blocked: null, kind: null, reason: '域名是否解析到内网，要发送后由服务端判断。' }
  }

  return { blocked: false, kind: null, reason: '' }
}

function hostOf(raw: string): string {
  try {
    return new URL(raw.trim()).hostname.replace(/^\[|\]$/g, '')
  } catch {
    return ''
  }
}

/**
 * True when an address can only be reached from this machine.
 *
 * This is the number one reason "I configured the webhook but nothing arrives": people
 * paste a localhost URL into GitHub or Stripe, and nobody tells them it cannot work.
 */
export function isLocalAddress(url: string): boolean {
  try {
    return isReserved(new URL(url).hostname.replace(/^\[|\]$/g, ''))
  } catch {
    return false
  }
}

function isIPLiteral(host: string): boolean {
  if (host.includes(':')) {
    return true // IPv6
  }

  return /^\d{1,3}(\.\d{1,3}){3}$/.test(host)
}

function isReserved(host: string): boolean {
  if (host === 'localhost' || host.endsWith('.localhost')) {
    return true
  }

  if (host.includes(':')) {
    return isReservedIPv6(host)
  }

  return isReservedIPv4(host)
}

function isReservedIPv4(host: string): boolean {
  const parts = host.split('.')
  if (parts.length !== 4) {
    return false
  }

  const octets = parts.map((p) => (p === '' ? NaN : Number(p)))
  if (octets.some((n) => !Number.isInteger(n) || n < 0 || n > 255)) {
    return false
  }

  const [a, b] = octets

  if (a === 0) return true // 0.0.0.0/8
  if (a === 10) return true // 10/8
  if (a === 127) return true // loopback
  if (a === 169 && b === 254) return true // link-local, incl. cloud metadata
  if (a === 172 && b >= 16 && b <= 31) return true // 172.16/12
  if (a === 192 && b === 168) return true // 192.168/16
  if (a === 100 && b >= 64 && b <= 127) return true // 100.64/10 (CGNAT)
  if (a === 192 && b === 0 && octets[2] === 0) return true // 192.0.0/24
  if (a === 198 && (b === 18 || b === 19)) return true // 198.18/15 benchmark
  if (a >= 224) return true // multicast + reserved + broadcast

  return false
}

function isReservedIPv6(host: string): boolean {
  const h = host.toLowerCase()

  if (h === '::' || h === '::1') {
    return true
  }

  // IPv4-mapped (::ffff:127.0.0.1) - judge the embedded address.
  const mapped = h.match(/^::ffff:(\d+\.\d+\.\d+\.\d+)$/)
  if (mapped) {
    return isReservedIPv4(mapped[1])
  }

  return (
    h.startsWith('fc') ||
    h.startsWith('fd') || // fc00::/7 unique local
    h.startsWith('fe80') || // link local
    h.startsWith('2002:') || // 6to4
    h.startsWith('64:ff9b') || // NAT64
    h.startsWith('ff') // multicast
  )
}
