import { CodeHighlight } from '@mantine/code-highlight'
import {
  Accordion,
  ActionIcon,
  Anchor,
  Box,
  Button,
  Card,
  Chip,
  Collapse,
  Grid,
  Group,
  Modal,
  SegmentedControl,
  Stack,
  Switch,
  Table,
  Tabs,
  Text,
  TextInput,
  Textarea,
  Title,
  Tooltip,
  useComputedColorScheme,
} from '@mantine/core'
import { useDisclosure, useHotkeys } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import {
  IconArrowLeft,
  IconInfoCircle,
  IconSend,
  IconTerminal2,
  IconTrash,
} from '@tabler/icons-react'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  ApiError,
  api,
  decodeBody,
  encodeBody,
  formatBytes,
  prettyPrint,
  type EventDetail,
  type ReplayAttempt,
  type ServerSettings,
} from '~/api/v1'
import {
  NEXT_STEP,
  OUTCOME_META,
  OutcomeBadge,
  nextStepForBlocked,
  outcomeColor,
  type Outcome,
} from '~/shared/components/outcome-badge'
import { CopyIconButton, CopyTextButton } from '~/shared/components/copy-button'
import { MethodBadge } from '~/shared/components/method-badge'
import { StatusBadge } from '~/shared/components/status-badge'
import { StateError, StateLoading } from '~/shared/components/states'
import { dayjs } from '~/shared/dayjs'
import { copyText } from '~/shared/utils/clipboard'
import { humanize } from '~/shared/utils/errors'
import { recentTargetsFrom } from '~/shared/utils/recent-targets'
import { willBeBlocked } from '~/shared/utils/target-check'
import { MONO } from '~/theme'
import { BADGE_TEXT_SHADE } from '~/theme/color'

const RECENT_TARGETS_KEY = 'whq.recentTargets'
const MAX_HISTORY = 20

const isValidTarget = (value: string): boolean => /^https?:\/\/\S+$/i.test(value.trim())

// ReplayReject is a refusal that never became a stored attempt.
//
// Anything that DID become an attempt (blocked, http_error, timeout, ...) is rendered by
// the result card instead, which is more informative and cannot contradict this.
type ReplayReject =
  | { kind: 'rate_limited'; outcome: Outcome }
  | { kind: 'invalid_target'; outcome: Outcome; message: string }
  | { kind: 'network_error'; outcome: Outcome; message: string }
  | { kind: 'server_error'; outcome: Outcome; message: string }

function classifyReject(err: unknown): ReplayReject {
  if (err instanceof ApiError) {
    if (err.status === 429) {
      return { kind: 'rate_limited', outcome: 'rate_limited' }
    }

    if (err.code === 'invalid_target' || err.status === 400) {
      return { kind: 'invalid_target', outcome: 'invalid_target', message: humanize(err) }
    }

    if (err.status >= 500) {
      return { kind: 'server_error', outcome: 'network_error', message: humanize(err) }
    }
  }

  return { kind: 'network_error', outcome: 'network_error', message: humanize(err) }
}

export function EventScreen(): React.JSX.Element {
  const { id = '', eid = '' } = useParams()
  const navigate = useNavigate()
  // The colour bars are drawn from the palette, so they need the scheme too: a step 8
  // bar is invisible on a dark background.
  const scheme = useComputedColorScheme('light')

  const [event, setEvent] = useState<EventDetail | null>(null)
  const [reveal, setReveal] = useState(false)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [view, setView] = useState<'pretty' | 'raw'>('pretty')
  const [headerFilter, setHeaderFilter] = useState('')

  const [target, setTarget] = useState('')
  const [targetTouched, setTargetTouched] = useState(false)
  const [sign, setSign] = useState(false)
  const [editMode, setEditMode] = useState(false)
  const [editedBody, setEditedBody] = useState<string | null>(null)
  const [advancedOpened, { toggle: toggleAdvanced }] = useDisclosure(false)
  const [busy, setBusy] = useState(false)
  const [attempts, setAttempts] = useState<ReplayAttempt[]>([])

  // A replay failure lives here, never in `error`: taking the page down would also take
  // the captured request away, which is the one thing the user still needs to see.
  //
  // It only holds refusals that produced NO attempt (rate limit, bad address, 5xx). A
  // refusal that did get recorded - a blocked target, for instance - is already shown by
  // the result card, and rendering both would put two contradicting verdicts on screen.
  const [replayReject, setReplayReject] = useState<ReplayReject | null>(null)
  const [cooldown, setCooldown] = useState(0)
  const [recent, setRecent] = useState<string[]>([])
  const [deleteOpened, { open: openDelete, close: closeDelete }] = useDisclosure(false)

  // Server limits (timeout, allow list) decide what the replay panel may warn about.
  const [serverSettings, setServerSettings] = useState<ServerSettings | null>(null)

  useEffect(() => {
    void api
      .settings()
      .then(setServerSettings)
      .catch(() => setServerSettings(null))
  }, [])

  const targetRef = useRef<HTMLInputElement>(null)

  // "r" jumps straight to the replay target - the action this page exists for.
  useHotkeys([['r', () => targetRef.current?.focus()]])

  // Count the rate-limit cooldown down to zero, then let the button be pressed again.
  const cooling = cooldown > 0

  useEffect(() => {
    if (!cooling) {
      return
    }

    const timer = window.setInterval(() => setCooldown((c) => (c <= 1 ? 0 : c - 1)), 1000)

    return () => window.clearInterval(timer)
  }, [cooling])

  const body = useMemo(() => (event ? decodeBody(event.body_base64) : ''), [event])
  const pretty = useMemo(() => prettyPrint(body), [body])
  const targetError = targetTouched && target.trim() !== '' && !isValidTarget(target) ? '请填写 http(s) 开头的完整地址' : undefined

  // `null` means "not edited yet"; '' is a real edit (an emptied body) and must survive
  // instead of being refilled from the original.
  const bodyText = editedBody ?? body

  const loadEvent = useCallback(async () => {
    try {
      setEvent(await api.getEvent(eid, reveal))
      setError(null)
    } catch (err) {
      setError(humanize(err))
    } finally {
      setLoading(false)
    }
  }, [eid, reveal])

  // Returns whether the history holds at least one attempt: the caller uses it to decide
  // between "show an error" and "the result card already says it".
  const loadReplays = useCallback(async (): Promise<boolean> => {
    try {
      const res = await api.listReplays(eid)

      setAttempts(res.items)

      if (res.items.length > 0 && target === '') {
        setTarget(res.items[0].target_url)
      }

      return res.items.length > 0
    } catch {
      /* history is optional */
      return false
    }
  }, [eid, target])

  // Re-fetch when the reveal toggle flips. It has to be its own effect: the first one is
  // keyed on the event id only so that switching tabs does not reload the page, but that
  // also means a state change alone would never trigger a request - which used to leave
  // the "show original value" switch doing nothing at all.
  const firstLoad = useRef(true)

  useEffect(() => {
    if (firstLoad.current) {
      firstLoad.current = false

      return
    }

    void loadEvent()
  }, [loadEvent])

  useEffect(() => {
    setEditedBody(null) // a different event means no pending edit

    void loadEvent()
    void loadReplays()

    const stored = window.localStorage.getItem(RECENT_TARGETS_KEY)
    if (stored) {
      try {
        setRecent(JSON.parse(stored) as string[])
      } catch {
        /* ignore */
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [eid])

  const runReplay = async () => {
    if (!isValidTarget(target)) {
      setTargetTouched(true)

      return
    }

    setBusy(true)

    try {
      const res = await api.replay(eid, {
        target_url: target.trim(),
        sign,
        // Base64 keeps binary payloads byte-exact; a JSON string would replace invalid
        // UTF-8 with U+FFFD and break the receiver's signature check.
        ...(editMode ? { body_base64: encodeBody(bodyText) } : {}),
      })

      setAttempts(res.attempts)
      setReplayReject(null)

      const next = [target.trim(), ...recent.filter((t) => t !== target.trim())].slice(0, 5)
      setRecent(next)
      window.localStorage.setItem(RECENT_TARGETS_KEY, JSON.stringify(next))
    } catch (err) {
      // Refresh first: the server records refusals too, so a blocked target has an
      // attempt by now. When it does, the result card is the better explanation and an
      // error card next to it would only contradict it.
      const recorded = await loadReplays()
      const code = err instanceof ApiError ? err.code : ''

      // A malformed address is stored as `blocked`, so the result card would mislabel it
      // as "try a public address" - which is the wrong remedy. Refusals that the recorded
      // attempt cannot express have to come from here instead.
      if (recorded && code !== 'invalid_target') {
        setReplayReject(null)
      } else {
        setReplayReject(classifyReject(err))

        if (err instanceof ApiError && err.retryAfter) {
          setCooldown(err.retryAfter)
        }
      }
    } finally {
      setBusy(false)
    }
  }

  const copyAsCurl = async () => {
    try {
      const curl = await api.exportCurl(eid, reveal)

      notifications.show(
        (await copyText(curl))
          ? { message: '已复制 cURL 命令' }
          : { color: 'yellow', message: '无法自动复制，请在弹出的窗口里手动复制' },
      )
    } catch (err) {
      notifications.show({ color: 'red', message: humanize(err) })
    }
  }

  const removeEvent = async () => {
    try {
      await api.deleteEvent(eid)
      notifications.show({ message: '已删除该事件' })

      // Client side navigation: a full reload would drop the SPA and the live stream.
      navigate(`/inboxes/${id}`)
    } catch (err) {
      notifications.show({ color: 'red', message: humanize(err) })
    }
  }

  if (loading && !event) {
    return <StateLoading rows={8} />
  }

  if (error || !event) {
    return <StateError message={error ?? '事件不存在'} onRetry={() => void loadEvent()} />
  }

  const last = attempts[0]

  // What this instance will do with the address, before anything is sent: it only
  // explains, the replay button stays enabled - refusing is the server's call, not ours.
  const targetVerdict = willBeBlocked(target, serverSettings)
  const timeoutSeconds = serverSettings ? Math.round(serverSettings.replay_timeout_ms / 1000) : 10
  const previewMax = serverSettings ? formatBytes(serverSettings.replay_max_preview) : '4 KiB'

  // Addresses this event was already replayed at (plus whatever was typed in this
  // browser) - replaying the same local service over and over is the normal workflow.
  const targetHistory = recentTargetsFrom(attempts, recent)

  // Revealing a secret requires access control; without it the API is open and the server
  // ignores the request entirely. Only disable when we positively know it is off: if the
  // settings call failed, leaving the switch usable is the lesser evil.
  const revealDisabled = serverSettings?.auth_enabled === false

  const headers = headerFilter
    ? event.headers.filter(
        (h) =>
          h.name.toLowerCase().includes(headerFilter.toLowerCase()) ||
          h.value.toLowerCase().includes(headerFilter.toLowerCase()),
      )
    : event.headers

  const replayPanel = (
    <Stack gap="sm">
      <Card withBorder padding="md">
        <Stack gap="xs">
          <Title order={5}>重放</Title>

          <TextInput
            ref={targetRef}
            placeholder="https://example.com/webhook（r 聚焦）"
            value={target}
            onChange={(e) => setTarget(e.currentTarget.value)}
            onBlur={() => setTargetTouched(true)}
            // Say what this instance will do with the address before it is sent: a
            // reserved address is the single most common reason the first replay fails,
            // and finding out afterwards costs minutes of confused debugging.
            error={targetVerdict.kind === 'invalid' ? targetVerdict.reason : targetError}
            description={targetVerdict.blocked === null ? targetVerdict.reason : undefined}
            aria-label="重放目标地址"
          />

          {/*
           * An explanation, never a roadblock: the replay button stays enabled, because
           * refusing the address is the server's decision. We only move the reason
           * forward in time.
           */}
          {targetVerdict.blocked === true && targetVerdict.kind !== 'invalid' && (
            <Stack gap={4}>
              <Text fz="xs" style={{ color: `var(--mantine-color-error-${BADGE_TEXT_SHADE[scheme]})` }}>
                {targetVerdict.reason}
              </Text>

              {/* The command, not a description of it: the fix for a refused address is a
                  startup flag, and retyping a flag is how typos get in. */}
              {targetVerdict.command && (
                <Group gap={4} wrap="nowrap">
                  <Text fz="xs" className="whq-mono" truncate style={{ flex: 1 }}>
                    {targetVerdict.command}
                  </Text>

                  <CopyIconButton value={targetVerdict.command} label="复制启动命令" />

                  <Anchor component={Link} to="/help#blocked" fz="xs">
                    为什么
                  </Anchor>
                </Group>
              )}
            </Stack>
          )}

          {targetVerdict.blocked === false && targetVerdict.kind === 'local' && (
            <Text fz="xs" c="dimmed">
              {targetVerdict.reason}
            </Text>
          )}

          {targetHistory.length > 0 && (
            <Group gap={4}>
              {targetHistory.map((t) => (
                <Tooltip
                  key={t.url}
                  label={
                    t.outcome
                      ? `上次结果：${OUTCOME_META[t.outcome]?.short ?? t.outcome}`
                      : '本机填过，还没有重放结果'
                  }
                >
                  <Chip size="xs" checked={t.url === target} onClick={() => setTarget(t.url)}>
                    <Group gap={4} wrap="nowrap">
                      {/* A dot, not a badge: the row stays scannable at a dozen entries. */}
                      {t.outcome !== null && (
                        <span
                          aria-hidden
                          style={{
                            width: 6,
                            height: 6,
                            borderRadius: 999,
                            background: outcomeColor(t.outcome, scheme),
                          }}
                        />
                      )}

                      {t.url.replace(/^https?:\/\//, '').slice(0, 28)}

                      {t.outcome !== null && (
                        <span className="visually-hidden">
                          上次结果 {OUTCOME_META[t.outcome]?.short ?? t.outcome}
                        </span>
                      )}
                    </Group>
                  </Chip>
                </Tooltip>
              ))}
            </Group>
          )}

          <Group justify="space-between">
            <Button
              size="xs"
              variant="subtle"
              color="gray"
              onClick={toggleAdvanced}
              aria-label="展开高级选项"
            >
              高级选项 {advancedOpened ? '▲' : '▼'}
            </Button>

            <Button
              size="sm"
              leftSection={<IconSend size={14} />}
              loading={busy}
              // Only a malformed address stops the button; a refused one is still allowed
              // through, because the server - not this input - is the one that decides.
              disabled={target.trim() === '' || targetVerdict.kind === 'invalid'}
              onClick={() => void runReplay()}
            >
              重放
            </Button>
          </Group>

          <Collapse in={advancedOpened}>
            <Stack gap="xs">
              <Switch
                size="xs"
                checked={editMode}
                onChange={(e) => setEditMode(e.currentTarget.checked)}
                label="编辑请求体后重放（原始事件不会被修改）"
              />

              {editMode && (
                <Textarea
                  minRows={5}
                  value={bodyText}
                  onChange={(e) => setEditedBody(e.currentTarget.value)}
                  styles={{ input: { fontFamily: MONO, fontSize: 12 } }}
                  aria-label="编辑请求体"
                />
              )}

              <Switch
                size="xs"
                checked={sign}
                onChange={(e) => setSign(e.currentTarget.checked)}
                label="用收件箱密钥重新签名"
              />

            </Stack>
          </Collapse>

          {/* Always visible: the rules that decide whether a replay works at all. Hidden
              inside "advanced options" they were discovered only after a failure. */}
          <Text fz="xs" c="dimmed">
            只转发原始请求体与 Content-Type；敏感头（Authorization / Cookie / X-API-Key）不转发；目标必须是不在内网的
            http(s) 地址；超时 {timeoutSeconds} 秒。
            <Anchor component={Link} to="/help#replay" fz="xs">
              {' '}
              完整规则
            </Anchor>
          </Text>
        </Stack>
      </Card>

      {replayReject && (
        <Card
          withBorder
          padding="md"
          style={{ borderLeft: `3px solid ${outcomeColor(replayReject.outcome, scheme)}` }}
        >
          <Stack gap="xs">
            <Group justify="space-between" wrap="nowrap">
              {replayReject.kind === 'rate_limited' || replayReject.kind === 'invalid_target' ? (
                <OutcomeBadge outcome={replayReject.outcome} full />
              ) : (
                <Text fz="sm" fw={600}>
                  没有发起重放
                </Text>
              )}

              {cooldown > 0 && (
                <Text fz="xs" c="dimmed" className="whq-mono">
                  {cooldown}s
                </Text>
              )}
            </Group>

            <Text fz="xs" c="dimmed">
              {replayReject.kind === 'rate_limited' ? NEXT_STEP.rate_limited : replayReject.message}
            </Text>

            <Button
              size="xs"
              variant="light"
              loading={busy}
              disabled={cooldown > 0}
              onClick={() => void runReplay()}
            >
              {/* Same verb as the main action: two words for one action reads as two
                  different actions. */}
              {cooldown > 0 ? `${cooldown} 秒后可重试` : '重新重放'}
            </Button>
          </Stack>
        </Card>
      )}

      {/* When the address itself was rejected, the error card is the accurate one - the
          stored attempt only knows it was "blocked". */}
      {last && replayReject?.kind !== 'invalid_target' && (
        <Card
          withBorder
          padding="md"
          style={{ borderLeft: `3px solid ${outcomeColor(last.outcome, scheme)}` }}
        >
          <Stack gap="xs">
            <Group justify="space-between" wrap="nowrap">
              <OutcomeBadge outcome={last.outcome} statusCode={last.status_code} full />

              <Text fz="xs" c="dimmed" className="whq-mono">
                {last.duration_ms} ms
              </Text>
            </Group>

            <Group gap="xs" wrap="nowrap">
              <Text fz="xs" c="dimmed" className="whq-mono" truncate style={{ flex: 1 }}>
                {last.target_url}
              </Text>

              <Text fz="xs" c="dimmed">
                {dayjs(last.started_at).format('HH:mm:ss')}
              </Text>
            </Group>

            <Text fz="xs" c="dimmed">
              {/* A refused target gets the remedy for that exact address - the same
                  sentence the input shows before sending, so the two cannot disagree. */}
              {last.outcome === 'blocked'
                ? nextStepForBlocked(last.target_url, serverSettings)
                : NEXT_STEP[last.outcome as Outcome] ?? ''}
            </Text>

            {/* Kept verbatim and copyable: it is how you tell "malformed address" from
                "blocked" once the in-memory error card is gone. Folded by default because
                it is server wording in a Chinese UI. */}
            {last.error && (
              <Accordion variant="contained">
                <Accordion.Item value="raw">
                  <Accordion.Control>服务端返回的原始信息</Accordion.Control>

                  <Accordion.Panel>
                    <CodeHighlight code={last.error} language="plaintext" radius="sm" />
                  </Accordion.Panel>
                </Accordion.Item>
              </Accordion>
            )}

            {last.preview_truncated && (
              <Text fz="xs" c="dimmed">
                仅保存前 {previewMax}；你的服务收到的是完整请求，判断成败看状态码。
              </Text>
            )}

            {last.response_preview && (
              <CodeHighlight
                code={last.response_preview + (last.preview_truncated ? '\n…（响应过长，已截断）' : '')}
                language="json"
                radius="sm"
                maxCollapsedHeight={160}
                withCopyButton
                copyLabel="复制响应"
                copiedLabel="已复制"
              />
            )}
          </Stack>
        </Card>
      )}

      {attempts.length > 0 && (
        <Card withBorder padding="md">
          <Stack gap={6}>
            <Group gap={4}>
              <Text fz="sm" fw={600}>
                重放历史（{attempts.length}）
              </Text>

              {/* The history cannot tell "malformed address" from "blocked" (both are
                  stored as blocked), so say where the precise reason actually lives -
                  somewhere that is reachable even after a refresh. */}
              <Tooltip
                label="地址不合法的请求在重放历史里也记成「被拦截」——服务端目前不区分这两类。当次的结果卡或错误卡最准；刷新过的话，展开结果卡里的「服务端返回的原始信息」也能看到具体原因。"
                maw={320}
                multiline
              >
                <ActionIcon size="xs" variant="subtle" aria-label="关于重放历史的结果标记">
                  <IconInfoCircle size={12} />
                </ActionIcon>
              </Tooltip>
            </Group>

            {attempts.slice(0, MAX_HISTORY).map((a) => (
              <Group key={a.id} justify="space-between" wrap="nowrap" gap="xs">
                <OutcomeBadge outcome={a.outcome} statusCode={a.status_code} />

                <Text fz="xs" c="dimmed" truncate style={{ flex: 1 }} className="whq-mono">
                  {a.target_url.replace(/^https?:\/\//, '')}
                </Text>

                {a.edited && (
                  <StatusBadge size="xs" color="gray.8">
                    已编辑
                  </StatusBadge>
                )}

                {a.attempt_no > 1 && (
                  <StatusBadge size="xs" color="gray.8">
                    第 {a.attempt_no} 次
                  </StatusBadge>
                )}

                <Text fz="xs" c="dimmed" className="whq-mono">
                  {a.duration_ms}ms
                </Text>

                <Text fz="xs" c="dimmed">
                  {dayjs(a.started_at).format('HH:mm:ss')}
                </Text>
              </Group>
            ))}

            {attempts.length > MAX_HISTORY && (
              <Text fz="xs" c="dimmed">
                仅显示最近 {MAX_HISTORY} 条
              </Text>
            )}
          </Stack>
        </Card>
      )}
    </Stack>
  )

  return (
    <Stack gap="md" className="whq-enter">
      <Group justify="space-between" align="flex-start">
        <Stack gap={2}>
          <Anchor component={Link} to={`/inboxes/${id}`} fz="sm" c="dimmed">
            <IconArrowLeft size={14} /> 事件列表
          </Anchor>

          <Group gap="xs">
            {/* A webhook path is one long unbroken token: without break-all it pushes the
                header wider than a phone screen. */}
            {/*
             * Same level as the other two screens (order 4) so the three pages share one
             * visual anchor.
             *
             * The monospace stack is set inline and not through `.whq-mono`: that class
             * also pins `font-size: 12px`, which silently demoted this title to 12px and
             * left the event page without a heading while the other two had a 16px one.
             * Clamped like the inbox title - a path is one long token and would otherwise
             * push the action buttons off the header.
             */}
            <Title
              order={4}
              lineClamp={2}
              style={{ fontFamily: 'var(--whq-mono)', wordBreak: 'break-all' }}
            >
              {event.method} {event.path}
            </Title>

            <MethodBadge method={event.method} />

            {event.signature_valid !== null && (
              <StatusBadge color={event.signature_valid ? 'success.8' : 'error.8'}>
                {event.signature_valid ? '签名有效' : '签名无效'}
              </StatusBadge>
            )}
          </Group>

          <Text fz="xs" c="dimmed">
            {dayjs(event.created_at).format('YYYY-MM-DD HH:mm:ss')}（{dayjs(event.created_at).fromNow()}） ·{' '}
            {formatBytes(event.body_size)} · 来源 {event.client_ip || '—'}
          </Text>
        </Stack>

        <Group gap="xs">
          <Button size="xs" variant="default" leftSection={<IconTerminal2 size={13} />} onClick={() => void copyAsCurl()}>
            复制为 cURL
          </Button>

          <Button size="xs" variant="subtle" color="red" leftSection={<IconTrash size={13} />} onClick={openDelete}>
            删除
          </Button>
        </Group>
      </Group>

      <Grid gutter="md">
        <Grid.Col span={{ base: 12, lg: 8 }} order={{ base: 2, lg: 1 }}>
          <Card withBorder>
            <Tabs defaultValue="body">
              <Tabs.List>
                <Tabs.Tab value="body">请求体</Tabs.Tab>
                <Tabs.Tab value="headers">请求头（{event.headers.length}）</Tabs.Tab>
                <Tabs.Tab value="query">Query 参数</Tabs.Tab>
              </Tabs.List>

              <Tabs.Panel value="body" pt="sm">
                <Stack gap="xs">
                  <Group justify="space-between">
                    <SegmentedControl
                      size="xs"
                      value={view}
                      onChange={(v) => setView(v as 'pretty' | 'raw')}
                      data={[
                        { value: 'pretty', label: '美化' },
                        { value: 'raw', label: '原始' },
                      ]}
                    />

                    <Group gap={4}>
                      <Text fz="xs" c="dimmed">
                        {event.content_type || '无 Content-Type'} · {formatBytes(event.body_size)}
                        {pretty === null && ' · 非合法 JSON，按原文展示'}
                      </Text>

                      <CopyTextButton value={view === 'pretty' ? pretty ?? body : body} label="复制" />
                    </Group>
                  </Group>

                  <CodeHighlight
                    code={view === 'pretty' ? pretty ?? body : body}
                    language={pretty === null ? 'plaintext' : 'json'}
                    withCopyButton
                    copyLabel="复制"
                    copiedLabel="已复制"
                    maxCollapsedHeight={360}
                    radius="sm"
                  />
                </Stack>
              </Tabs.Panel>

              <Tabs.Panel value="headers" pt="sm">
                <Stack gap="xs">
                  <Group justify="space-between">
                    <TextInput
                      placeholder="过滤请求头"
                      size="xs"
                      value={headerFilter}
                      onChange={(e) => setHeaderFilter(e.currentTarget.value)}
                      style={{ maxWidth: 260 }}
                      aria-label="过滤请求头"
                    />

                    <Switch
                      size="xs"
                      checked={reveal}
                      onChange={(e) => setReveal(e.currentTarget.checked)}
                      label="显示敏感头原始值"
                      disabled={!event.headers.some((h) => h.sensitive) || revealDisabled}
                    />
                  </Group>

                  <Text fz="xs" c="dimmed">
                    敏感请求头默认以掩码展示（数据库中也不存明文）
                    {serverSettings?.auth_enabled === false ? '' : '；开启后会记录一条审计日志'}。
                  </Text>

                  {/* Two different reasons the switch cannot help - say which one it is,
                      instead of letting the user flip it and see nothing happen. */}
                  {serverSettings !== null && !serverSettings.auth_enabled && (
                    <Text fz="xs" c="dimmed">
                      服务端未启用访问控制，出于安全考虑不提供解密显示（需 --auth-token 或 --auth-keys）。
                    </Text>
                  )}

                  {serverSettings?.auth_enabled && !serverSettings.encryption_enabled && (
                    <Text fz="xs" c="red.7">
                      服务端未配置加密密钥：敏感头入库时已存为掩码，原始值不可恢复。
                    </Text>
                  )}

                  <Table>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>名称</Table.Th>
                        <Table.Th>值</Table.Th>
                      </Table.Tr>
                    </Table.Thead>

                    <Table.Tbody>
                      {headers.map((h, i) => (
                        <Table.Tr key={`${h.name}-${i}`}>
                          <Table.Td>
                            <Group gap={4}>
                              <Text fz="xs" className="whq-mono">
                                {h.name}
                              </Text>

                              {h.sensitive && (
                                <StatusBadge size="xs" color="gray.8">
                                  敏感
                                </StatusBadge>
                              )}
                            </Group>
                          </Table.Td>

                          <Table.Td>
                            {/* `revealed` is the server telling us it actually decrypted
                                something. With reveal on but revealed=false the value is
                                still the mask - meaning the original was never stored
                                (no encryption key at capture time). Without this the
                                value looks exactly like a real secret and the user
                                believes they are looking at the plaintext. */}
                            {h.sensitive && reveal && !h.revealed ? (
                              <Group gap={6} wrap="nowrap">
                                <Text
                                  fz="xs"
                                  className="whq-mono"
                                  c="dimmed"
                                  style={{ wordBreak: 'break-word' }}
                                >
                                  {h.value}
                                </Text>

                                <StatusBadge size="xs" color="warning.8">
                                  原值不可恢复
                                </StatusBadge>
                              </Group>
                            ) : (
                              <Text
                                fz="xs"
                                className="whq-mono"
                                c={h.sensitive && !reveal ? 'dimmed' : undefined}
                                style={{ wordBreak: 'break-word' }}
                              >
                                {h.value}
                              </Text>
                            )}
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                </Stack>
              </Tabs.Panel>

              <Tabs.Panel value="query" pt="sm">
                {event.query ? (
                  <Table>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>参数</Table.Th>
                        <Table.Th>值</Table.Th>
                      </Table.Tr>
                    </Table.Thead>

                    <Table.Tbody>
                      {Array.from(new URLSearchParams(event.query).entries()).map(([k, v]) => (
                        <Table.Tr key={k}>
                          <Table.Td>
                            <Text fz="xs" className="whq-mono">
                              {k}
                            </Text>
                          </Table.Td>

                          <Table.Td>
                            <Text fz="xs" className="whq-mono">
                              {v}
                            </Text>
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                ) : (
                  <Text fz="sm" c="dimmed" py="md">
                    这条请求没有 Query 参数。
                  </Text>
                )}
              </Tabs.Panel>
            </Tabs>
          </Card>
        </Grid.Col>

        {/* On small screens the replay panel comes first: it is the action the user came for. */}
        <Grid.Col span={{ base: 12, lg: 4 }} order={{ base: 1, lg: 2 }} style={{ alignSelf: 'start' }}>
          <Box pos={{ lg: 'sticky' }} top={72}>
            {replayPanel}
          </Box>
        </Grid.Col>
      </Grid>

      <Modal opened={deleteOpened} onClose={closeDelete} title="删除事件" size="sm">
        <Stack>
          <Text fz="sm">删除后这条事件及其重放历史都无法恢复。</Text>

          <Group justify="flex-end">
            <Button size="xs" variant="default" onClick={closeDelete}>
              取消
            </Button>

            <Button size="xs" color="red" onClick={() => void removeEvent()}>
              确认删除
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}
