import { describe, expect, it, it as _it } from 'vitest'
import { checkTarget, isLocalAddress, willBeBlocked, type TargetCheck, type TargetLevel } from './target-check'

type Row = {
  name: string
  input: string
  hosts?: string[]
  allowPrivate?: boolean
  level: TargetLevel
  /** Expected fragment of `message`; omit for a silent `ok`. */
  contains?: string
}

const ROWS: Row[] = [
  // ── error: the address is malformed, regardless of server config ─────────────
  { name: 'empty input stays quiet (the user just started typing)', input: '', level: 'ok' },
  { name: 'missing scheme', input: 'example.com/hook', level: 'error', contains: '地址不完整' },
  { name: 'empty host', input: 'http:///', level: 'error', contains: '地址不完整' },
  { name: 'ftp is not supported', input: 'ftp://example.com/h', level: 'error', contains: '不支持 ftp' },
  { name: 'mailto is not supported', input: 'mailto:a@b.com', level: 'error', contains: '不支持 mailto' },
  { name: 'credentials in the url', input: 'http://user:pass@example.com/', level: 'error', contains: '不能带用户名和密码' },
  { name: 'username only', input: 'http://user@example.com/', level: 'error', contains: '不能带用户名和密码' },

  // ── ok: public addresses (a name carries a "who decides" note) ───────────────
  { name: 'public host name', input: 'https://example.com/hook', level: 'ok', contains: '服务端判断' },
  { name: 'public host with port', input: 'https://example.com:8443/hook', level: 'ok', contains: '服务端判断' },
  { name: 'public ip', input: 'http://1.2.3.4/h', level: 'ok' },
  { name: 'four non numeric segments is not an ip', input: 'http://a.b.c.d/h', level: 'ok', contains: '服务端判断' },

  // ── warn: reserved addresses ────────────────────────────────────────────────
  { name: 'loopback', input: 'http://127.0.0.1:9099/ok', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'localhost', input: 'http://localhost:9099/ok', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'localhost is case insensitive', input: 'http://LOCALHOST/a', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'ipv6 loopback', input: 'http://[::1]/a', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'rfc1918 10/8', input: 'http://10.0.0.5/h', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'rfc1918 192.168/16', input: 'http://192.168.1.10/h', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'rfc1918 172.16/12', input: 'http://172.16.0.1/h', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'cloud metadata 169.254', input: 'http://169.254.169.254/latest/meta-data/', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'cgnat 100.64/10', input: 'http://100.64.0.1/h', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'unspecified 0.0.0.0/8', input: 'http://0.0.0.0/h', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'multicast 224/4', input: 'http://224.0.0.1/h', level: 'warn', contains: '内网 / 本机地址' },
  { name: 'ipv6 ula', input: 'http://[fd00::1]/h', level: 'warn', contains: '内网 / 本机地址' },

  // ── allow list x allow-private: both switches are required ───────────────────
  {
    name: 'listed host + allow-private passes',
    input: 'http://127.0.0.1:9099/ok',
    hosts: ['127.0.0.1'],
    allowPrivate: true,
    level: 'ok',
  },
  {
    name: 'listed host without allow-private still warns',
    input: 'http://127.0.0.1:9099/ok',
    hosts: ['127.0.0.1'],
    allowPrivate: false,
    level: 'warn',
    contains: '内网 / 本机地址',
  },
  {
    name: 'allow-private without being listed still warns',
    input: 'http://127.0.0.1:9099/ok',
    hosts: ['10.0.0.5'],
    allowPrivate: true,
    level: 'warn',
    contains: '不在服务端的允许名单里',
  },
  {
    name: 'the allow list is case insensitive',
    input: 'http://LOCALHOST/a',
    hosts: ['LocalHost'],
    allowPrivate: true,
    level: 'ok',
  },
  {
    name: 'a public host outside the list is refused too',
    input: 'https://example.com/hook',
    hosts: ['other.com'],
    level: 'warn',
    contains: '不在服务端的允许名单里',
  },
]

describe('checkTarget', () => {
  it.each(ROWS)('$name', ({ input, hosts, allowPrivate, level, contains }) => {
    const got: TargetCheck = checkTarget(input, hosts ?? [], allowPrivate ?? false)

    expect(got.level).toBe(level)

    if (contains) {
      expect(got.message ?? '').toContain(contains)
    } else {
      expect(got.message).toBeUndefined()
    }
  })

  it('only refuses to send for a malformed address, never for a warning', () => {
    // A warning is a hint: only the server knows the resolved address, so it decides.
    expect(checkTarget('http://10.0.0.1/h').level).not.toBe('error')
    expect(checkTarget('ftp://x/y').level).toBe('error')
  })

  describe('isLocalAddress', () => {
    it('recognises the addresses a third party could never reach', () => {
      expect(isLocalAddress('http://127.0.0.1/x')).toBe(true)
      expect(isLocalAddress('http://localhost/x')).toBe(true)
      expect(isLocalAddress('http://10.1.2.3/x')).toBe(true)
      expect(isLocalAddress('https://example.com/x')).toBe(false)
      expect(isLocalAddress('not a url')).toBe(false)
    })
  })
})

describe('willBeBlocked', () => {
  // The server is the authority: every case below is decided by the settings an instance
  // was actually started with, never by something the client makes up.
  const settings = (hosts: string[] = [], allowPrivate = false) => ({ replay_allow_hosts: hosts, replay_allow_private: allowPrivate })

  it('says nothing before the user has typed anything', () => {
    expect(willBeBlocked('', settings())).toEqual({ blocked: false, kind: null, reason: '' })
  })

  it('reports a malformed address without promising a verdict from the server', () => {
    const v = willBeBlocked('not a url', settings())

    expect(v.blocked).toBe(true)
    expect(v.kind).toBe('invalid')
    expect(v.reason).toContain('地址不完整')
  })

  it('refuses a reserved address on a plain instance, and names the flags that change it', () => {
    // The everyday case: somebody replays at their own laptop. It will be refused, and
    // the only useful answer is "here is how to start an instance that allows it".
    for (const url of [
      'http://127.0.0.1:3000/webhook',
      'http://10.0.0.5/h',
      'http://192.168.1.10/h',
      'http://169.254.169.254/latest/meta-data/',
    ]) {
      const v = willBeBlocked(url, settings())

      expect(v.blocked, url).toBe(true)
      expect(v.kind, url).toBe('local')
      expect(v.reason, url).toContain('target_blocked')
      expect(v.reason, url).toContain('--replay-allow-private')
    }
  })

  it('puts the host the user actually typed into the remedy', () => {
    // A copy-pasteable command beats a description of one.
    expect(willBeBlocked('http://10.0.0.5/h', settings()).reason).toContain('--replay-allow-host 10.0.0.5')
  })

  it('still refuses a reserved address that is listed but not allowed as private', () => {
    // The trap: being on the allow list is not enough for a reserved address. Without
    // --replay-allow-private it is refused all the same, so saying "allowed" here would
    // be a lie.
    const v = willBeBlocked('http://127.0.0.1:3000/webhook', settings(['127.0.0.1'], false))

    expect(v.blocked).toBe(true)
    expect(v.kind).toBe('local')
  })

  it('clears a reserved address once this instance allows both', () => {
    const v = willBeBlocked('http://127.0.0.1:3000/webhook', settings(['127.0.0.1'], true))

    expect(v.blocked).toBe(false)
    expect(v.kind).toBe('local')
    expect(v.reason).toContain('已被当前实例放行')
  })

  it('refuses a host outside the allow list, without calling it an internal address', () => {
    // Same outcome (the server refuses) but a different reason, and the reason is what
    // tells the user what to do next.
    const v = willBeBlocked('https://example.com/hook', settings(['127.0.0.1'], true))

    expect(v.blocked).toBe(true)
    expect(v.kind).toBe('not-allowed')
    expect(v.reason).toContain('不在服务端的允许名单里')
  })

  it('admits it cannot know what a host name resolves to', () => {
    // A name is resolved by the server, so "blocked: null" is the honest answer - the
    // panel must not turn that into a promise.
    const v = willBeBlocked('https://example.com/hook', settings())

    expect(v.blocked).toBeNull()
    expect(v.reason).toContain('服务端判断')
  })

  it('clears a public address on an instance without an allow list', () => {
    expect(willBeBlocked('http://1.2.3.4/h', settings())).toEqual({ blocked: false, kind: null, reason: '' })
  })

  it('treats missing settings as "no allow list" instead of guessing', () => {
    expect(willBeBlocked('http://127.0.0.1/h', null).blocked).toBe(true)
    expect(willBeBlocked('http://127.0.0.1/h', undefined).blocked).toBe(true)
  })

  it('ignores case and stray spaces in the allow list', () => {
    expect(willBeBlocked('http://127.0.0.1/h', settings([' 127.0.0.1 '], true)).blocked).toBe(false)
  })

  it('hands over a command that can be pasted, not a description of one', () => {
    // `webhook-zq start` is the real binary and subcommand: a made up name would be worse
    // than no hint, because the user would copy it and get "command not found".
    expect(willBeBlocked('http://127.0.0.1:3000/h', settings()).command).toBe(
      'webhook-zq start --replay-allow-host 127.0.0.1 --replay-allow-private',
    )
  })

  it('asks only for the host when the address is public but not listed', () => {
    // Adding --replay-allow-private for a public host is noise, and it widens the policy
    // for no reason.
    expect(willBeBlocked('https://example.com/hook', settings(['127.0.0.1'], true)).command).toBe(
      'webhook-zq start --replay-allow-host example.com',
    )
  })

  it('offers no command when there is nothing to fix', () => {
    expect(willBeBlocked('http://1.2.3.4/h', settings()).command).toBeUndefined()
    expect(willBeBlocked('https://example.com/hook', settings()).command).toBeUndefined()
    expect(willBeBlocked('', settings()).command).toBeUndefined()
  })
})

void _it
