/**
 * Hand written client for the JSON API (no OpenAPI code generation).
 *
 * Why hand written: the upstream project generated both the Go server stubs and this
 * client from `api/openapi.yml`, which means a fresh clone cannot be built until a code
 * generation step has run. Keeping the contract explicit here (and mirroring it in
 * `internal/httpapi`) makes the project buildable and reviewable as-is.
 *
 * Every function maps 1:1 to a route defined in internal/httpapi/api.go.
 */

const BASE = '/api/v1'

export type Inbox = {
  id: string
  name: string
  token: string
  enabled: boolean
  receive_url: string
  event_count: number
  last_event_at?: string
  created_at: string
  updated_at: string
  response_code: number
  response_delay_ms: number
  response_body_base64?: string
  signature_header: string
  signature_scheme: string
  require_signature: boolean
  has_signing_secret: boolean
  retention_max_events: number
  retention_max_days: number
}

export type EventListItem = {
  id: string
  inbox_id: string
  method: string
  path: string
  query: string
  content_type: string
  body_size: number
  preview: string
  client_ip: string
  signature_valid: boolean | null
  created_at: string
  replay_count: number
  last_outcome?: string
  last_status_code?: number
  last_replayed_at?: string
}

export type HeaderView = {
  name: string
  value: string
  sensitive: boolean
  /** True only when the server actually decrypted it; a mask while revealing means "gone". */
  revealed: boolean
}

export type EventDetail = {
  id: string
  inbox_id: string
  method: string
  path: string
  query: string
  content_type: string
  headers: HeaderView[]
  body_base64: string
  body_size: number
  client_ip: string
  signature_valid: boolean | null
  created_at: string
}

export type ReplayAttempt = {
  id: string
  event_id: string
  attempt_no: number
  retry_of?: string
  target_url: string
  sign_applied: boolean
  started_at: string
  finished_at?: string
  duration_ms: number
  status_code?: number
  response_preview?: string
  preview_truncated: boolean
  error?: string
  outcome: 'success' | 'http_error' | 'timeout' | 'network_error' | 'blocked'
  edited: boolean
}

export type ServerSettings = {
  max_request_body_size: number
  replay_timeout_ms: number
  replay_max_preview: number
  replay_max_redirects: number
  replay_max_retries: number
  replay_rate_limit: number
  retention_max_events: number
  retention_max_days: number
  auth_enabled: boolean
  encryption_enabled: boolean
  public_url_root: string
  /** Header names that are masked at rest and never forwarded, as configured server side. */
  sensitive_headers: string[]
  /** Hosts explicitly allowed as replay targets (empty = IP policy only). */
  replay_allow_hosts: string[]
  /**
   * Whether allow-listed hosts may resolve to reserved addresses.
   *
   * Turning this on widens the policy to your own network, so it belongs on a machine you
   * control - never on an instance reachable from the internet.
   */
  replay_allow_private: boolean
  /**
   * Version of the running binary, or `0.0.0@undefined` when it was started without the
   * version ldflags (`go run`, a plain `go build`).
   */
  version: string
  /** UTC build time, or `unknown` for the same reason. */
  build_time: string
}

export type Paged<T> = { items: T[]; total: number; limit: number; offset: number }

/**
 * Outcomes a stored replay attempt can have.
 *
 * `rate_limited` is deliberately NOT here: the limiter runs before an attempt is created,
 * so a throttled call never reaches the database. The UI shows it as its own state, but
 * it can never appear in the history list.
 */
export type AttemptOutcome = 'success' | 'http_error' | 'timeout' | 'network_error' | 'blocked'

export type EventFilters = {
  from?: string
  to?: string
  content_type?: string
  method?: string
  q?: string
  limit?: number
  offset?: number
}

export type ReplayInput = {
  target_url: string
  method?: string
  /**
   * An edited body travels as base64, not as a JSON string: `JSON.stringify` would
   * replace invalid UTF-8 with U+FFFD and break the receiver's signature check. The
   * server only accepts `body_base64` - there is no plain `body` field.
   */
  body_base64?: string
  headers?: { name: string; value: string }[]
  sign?: boolean
}

/** ApiError carries the structured error body produced by the Go API. */
export class ApiError extends Error {
  readonly status: number
  readonly code: string
  /**
   * Seconds the caller should wait before retrying, taken from the `Retry-After` header.
   * Only the rate limiter sets it; without it the UI can only say "failed".
   */
  readonly retryAfter?: number

  constructor(status: number, code: string, message: string, retryAfter?: number) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.retryAfter = retryAfter
  }
}

// retryAfter reads the header and falls back to the human sentence ("retry in 30s") so
// the countdown still works when a proxy strips the header.
function retryAfter(header: string | null, message?: string): number | undefined {
  const seconds = Number(header)
  if (header !== null && header !== '' && Number.isFinite(seconds)) {
    return seconds
  }

  const found = message?.match(/(\d+)\s*s\b/)

  return found ? Number(found[1]) : undefined
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(BASE + path, {
    ...init,
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  })

  if (res.status === 204) {
    return undefined as T
  }

  const text = await res.text()
  const payload = text ? (JSON.parse(text) as unknown) : null

  if (!res.ok) {
    const err = (payload as { error?: { code?: string; message?: string } } | null)?.error

    throw new ApiError(
      res.status,
      err?.code ?? 'unknown',
      err?.message ?? res.statusText,
      retryAfter(res.headers.get('Retry-After'), err?.message),
    )
  }

  return payload as T
}

// buildQuery serialises filter parameters, dropping empty values so the server sees its
// defaults instead of a pile of empty strings.
export const buildQuery = (
  params: Record<string, string | number | boolean | undefined>,
): string => {
  const usp = new URLSearchParams()

  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') {
      usp.set(k, String(v))
    }
  }

  const s = usp.toString()

  return s ? `?${s}` : ''
}

export const api = {
  settings: () => request<ServerSettings>('/settings'),

  listInboxes: (params: { q?: string; enabled?: boolean; limit?: number; offset?: number } = {}) =>
    request<Paged<Inbox>>('/inboxes' + buildQuery(params)),

  createInbox: (name: string) =>
    request<Inbox>('/inboxes', { method: 'POST', body: JSON.stringify({ name }) }),

  getInbox: (id: string) => request<Inbox>(`/inboxes/${id}`),

  patchInbox: (id: string, patch: Partial<Inbox> & { signing_secret?: string }) =>
    request<Inbox>(`/inboxes/${id}`, { method: 'PATCH', body: JSON.stringify(patch) }),

  deleteInbox: (id: string) => request<void>(`/inboxes/${id}`, { method: 'DELETE' }),

  rotateToken: (id: string) =>
    request<{ token: string; receive_url: string }>(`/inboxes/${id}/token/rotate`, { method: 'POST' }),

  listEvents: (inboxId: string, filters: EventFilters = {}) =>
    request<Paged<EventListItem>>(`/inboxes/${inboxId}/events` + buildQuery({ ...filters })),

  clearEvents: (inboxId: string) =>
    request<{ deleted: number }>(`/inboxes/${inboxId}/events`, { method: 'DELETE' }),

  getEvent: (id: string, reveal = false) =>
    request<EventDetail>(`/events/${id}` + buildQuery({ reveal })),

  deleteEvent: (id: string) => request<void>(`/events/${id}`, { method: 'DELETE' }),

  exportCurl: async (id: string, reveal = false): Promise<string> => {
    const res = await fetch(`${BASE}/events/${id}/export` + buildQuery({ format: 'curl', reveal }))
    if (!res.ok) {
      throw new ApiError(res.status, 'export_failed', await res.text())
    }

    return res.text()
  },

  replay: (eventId: string, input: ReplayInput) =>
    request<{ attempts: ReplayAttempt[]; last: ReplayAttempt }>(`/events/${eventId}/replay`, {
      method: 'POST',
      body: JSON.stringify(input),
    }),

  listReplays: (eventId: string) =>
    request<{ items: ReplayAttempt[] }>(`/events/${eventId}/replays`),
}

export type ConnectionState = 'connecting' | 'open' | 'closed'

/**
 * Realtime subscription over WebSocket.
 *
 * The server pushes metadata only (never the body), so the UI decides what to do: merge
 * the new event into the current page, or just tell the user that N events arrived.
 *
 * `onStatus` exists because a silently dead socket is worse than no socket at all - the
 * UI shows "reconnecting" instead of pretending everything is live.
 */
export function subscribe(
  inboxId: string,
  handlers: {
    onCreate: (msg: { action: string; event: { id: string } }) => void
    onStatus?: (state: ConnectionState) => void
  },
): () => void {
  const scheme = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const url = `${scheme}://${window.location.host}${BASE}/inboxes/${inboxId}/events/subscribe`
  const ws = new WebSocket(url)

  handlers.onStatus?.('connecting')

  ws.onopen = () => handlers.onStatus?.('open')
  ws.onclose = () => handlers.onStatus?.('closed')
  ws.onerror = () => handlers.onStatus?.('closed')

  ws.onmessage = (ev) => {
    try {
      handlers.onCreate(JSON.parse(ev.data as string))
    } catch {
      /* ignore malformed frames */
    }
  }

  return () => ws.close()
}

/** base64 -> UTF-8 text (bodies are transported as base64 to survive binary payloads). */
export function decodeBody(b64: string): string {
  if (!b64) {
    return ''
  }

  const binary = atob(b64)
  const bytes = new Uint8Array(binary.length)

  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }

  return new TextDecoder('utf-8').decode(bytes)
}

export function encodeBody(text: string): string {
  const bytes = new TextEncoder().encode(text)
  let binary = ''

  for (const b of bytes) {
    binary += String.fromCharCode(b)
  }

  return btoa(binary)
}

/** Pretty print JSON for display only - the stored body is never modified. */
export function prettyPrint(text: string): string | null {
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return null
  }
}

export function formatBytes(n: number): string {
  if (n < 1024) {
    return `${n} B`
  }

  if (n < 1024 * 1024) {
    return `${(n / 1024).toFixed(1)} KB`
  }

  return `${(n / 1024 / 1024).toFixed(2)} MB`
}
