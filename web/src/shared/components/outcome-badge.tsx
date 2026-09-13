import type React from 'react'
import { Badge, type MantineColor } from '@mantine/core'
import type { AttemptOutcome } from '~/api/v1'
import { BADGE_TEXT_SHADE } from '~/theme/color'
import { willBeBlocked, type PolicySettings } from '~/shared/utils/target-check'
import { StatusBadge } from './status-badge'
import {
  IconAlertTriangle,
  IconCircleCheck,
  IconClock,
  IconHourglass,
  IconPlugOff,
  IconShieldLock,
  type Icon,
} from '@tabler/icons-react'

/**
 * The outcomes the UI can show: everything a stored attempt can be, plus the client side
 * "the server refused to start this replay" state (rate limiting, policy refusal).
 */
export type Outcome =
  | AttemptOutcome
  // Not a target failure: the server refused to start the replay at all. It gets its own
  // state because the only correct reaction is "wait", not "fix the target".
  | 'rate_limited'
  // The address itself is malformed (protocol, credentials, missing host). The server
  // stores it as `blocked`, so the UI has to re-split it: the remedy is "fix how you
  // wrote it", not "change to a public address".
  | 'invalid_target'

/**
 * The outcomes a stored attempt can actually have - only these six.
 *
 * `invalid_target` is deliberately absent: it is persisted as `blocked`, so listing it
 * here would make the guide promise a state that never appears in the history.
 */
export const OUTCOME_LIST: Outcome[] = [
  'success',
  'http_error',
  'timeout',
  'network_error',
  'blocked',
  'rate_limited',
]

type Meta = {
  label: string
  short: string
  color: MantineColor
  variant: 'light' | 'outline'
  Icon: Icon
}

/**
 * One definition of the seven replay outcomes, used by both the event list and the
 * replay panel.
 *
 * Two rules behind this table:
 *   - every colour carries a step (8) and the badges paint their *text* from it
 *     themselves: a Mantine `light` badge is hardcoded to step 6 (see `BADGE_TEXT_SHADE`
 *     in theme/color.ts), and step 6 measured 2.2-3.0:1 here - i.e. the most important
 *     state in the product was the least readable thing on screen. Where Mantine's
 *     default palette has no dark enough step the theme's own palette is used (success
 *     instead of green) - same hue, just built with darker steps;
 *   - every outcome carries an icon, so the state is distinguishable without relying on
 *     colour alone.
 */
const META: Record<Outcome, Meta> = {
  success: {
    label: '重放成功，目标返回 2xx',
    short: '成功',
    // The theme's own green, not Mantine's: `green` tops out at 3.05:1 even at step 8.
    color: 'success.8',
    variant: 'light',
    Icon: IconCircleCheck,
  },
  http_error: {
    label: '目标已响应，但状态码不是 2xx',
    short: '非 2xx',
    color: 'warning.8',
    variant: 'light',
    Icon: IconAlertTriangle,
  },
  timeout: {
    label: '目标在超时时间内未完整响应',
    short: '超时',
    color: 'amber.8',
    variant: 'light',
    Icon: IconClock,
  },
  network_error: {
    label: '无法建立连接（DNS / 连接被拒 / TLS）',
    short: '网络错误',
    color: 'error.8',
    variant: 'light',
    Icon: IconPlugOff,
  },
  blocked: {
    label: '未发起请求：目标被安全策略拒绝',
    short: '被拦截',
    color: 'gray.8',
    variant: 'outline',
    Icon: IconShieldLock,
  },
  rate_limited: {
    label: '未发起请求：本调用方触发了重放限流',
    short: '已限流',
    color: 'amber.8',
    variant: 'light',
    Icon: IconHourglass,
  },
  invalid_target: {
    label: '目标地址不合法，服务端拒绝发起重放',
    short: '地址不合法',
    color: 'warning.8',
    variant: 'light',
    Icon: IconAlertTriangle,
  },
}

/**
 * Read only view of the table above.
 *
 * It exists so the mapping has exactly one definition: `OutcomeBadge` and `outcomeColor`
 * read the same object, and a test can assert the two agree without duplicating any
 * colour here (a copy would drift the moment somebody tweaks a shade).
 */
export const OUTCOME_META: Readonly<Record<Outcome, Meta>> = META

/** What to do next - "what happened + why + the next step", not just "it failed". */
export const NEXT_STEP: Record<Outcome, string> = {
  success: '目标已接受请求。可在重放历史里对比多次尝试的状态码与耗时。',
  http_error:
    '目标收到了请求但返回非 2xx：检查路径、方法与鉴权头是否正确（Authorization、Cookie 等不会被转发）。',
  timeout: '目标在超时时间内没有返回完整响应：确认服务可达，或调大服务的 replay-timeout。',
  network_error: '无法与目标建立连接：检查 DNS、端口与 TLS 证书；内网与保留地址会被拒绝。',
  // No "use a public address instead": the target is very often the user's own machine,
  // and telling them to move it to the internet is both wrong and unactionable. The
  // precise remedy is rendered by `nextStepForBlocked`, which knows the address.
  blocked:
    '请求未发出：目标命中安全策略（内网 / 链路本地 / 保留地址，或主机不在放行名单里）。要重放到这类地址，请用 --replay-allow-host <主机> 启动服务；内网 / 保留地址还要再加 --replay-allow-private。',
  rate_limited:
    '同一调用方每分钟可发起的重放次数有限（服务端 replay-rate-limit）。倒计时结束后可直接重试，或调大该参数后重启服务。',
  invalid_target:
    '地址本身写错了：必须以 http:// 或 https:// 开头、不能带用户名和密码、必须带主机名。改写法就行，与内网无关。',
}

/**
 * The "what now" for a refused target, tailored to the address that was actually used.
 *
 * The generic `NEXT_STEP.blocked` has to cover every cause, so it can only name the
 * flags. Here the address is known, which means the same function that warns while typing
 * (`willBeBlocked`) can name the host too - one source of truth for both places, so the
 * hint above the input and the verdict below it cannot drift apart.
 */
export function nextStepForBlocked(targetUrl: string, settings: PolicySettings): string {
  const verdict = willBeBlocked(targetUrl, settings)

  return verdict.kind === null ? NEXT_STEP.blocked : verdict.reason
}

export function OutcomeBadge({
  outcome,
  statusCode,
  full = false,
}: {
  outcome: string
  statusCode?: number
  full?: boolean
}): React.JSX.Element {
  const meta = META[outcome as Outcome]

  if (!meta) {
    // Never print the raw value: the server sends snake_case English, and dropping that
    // into a Chinese screen looks like a bug that survived to production.
    return <Badge variant="default">未知结果（{outcome}）</Badge>
  }

  return (
    <StatusBadge color={meta.color} variant={meta.variant} leftSection={<meta.Icon size={12} />}>
      {full ? meta.label : meta.short}
      {statusCode ? ` · ${statusCode}` : ''}
    </StatusBadge>
  )
}

/**
 * The colour token for an outcome, for callers that need a raw colour (a card border, a
 * chart) rather than a badge.
 *
 * This exists so the mapping lives in exactly one place: an earlier version had a second,
 * hand written ternary chain in the event screen, and the two drifted apart.
 */
export function outcomeColor(outcome: string, scheme: 'light' | 'dark'): string {
  // Mantine names its colour variables with a hyphen ("green-8"). The tokens above use a
  // dot because that is what the `color` prop wants, so it has to be converted here -
  // otherwise the browser drops the whole var() declaration and the bar never renders.
  const token = META[outcome as Outcome]?.color ?? 'gray.8'
  const [palette] = token.split('.')

  // Same step as the badge text: a step 8 bar on a dark background measures ~2.7:1,
  // which is exactly the mistake of assuming one step works in both schemes.
  return `var(--mantine-color-${palette}-${BADGE_TEXT_SHADE[scheme]})`
}
