import {
  ActionIcon,
  Anchor,
  Badge,
  Button,
  Card,
  Collapse,
  Group,
  Menu,
  Modal,
  Pagination,
  Select,
  SimpleGrid,
  Stack,
  Switch,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { useDebouncedValue, useDisclosure, useHotkeys } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import {
  IconArrowLeft,
  IconDots,
  IconFilter,
  IconSend,
  IconTerminal2,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'

import { Link, useNavigate, useParams } from 'react-router-dom'
import {
  ApiError,
  api,
  formatBytes,
  subscribe,
  type ConnectionState,
  type EventFilters,
  type EventListItem,
  type Inbox,
} from '~/api/v1'
import { OutcomeBadge } from '~/shared/components/outcome-badge'
import { CopyIconButton, CopyTextButton } from '~/shared/components/copy-button'
import { MethodBadge } from '~/shared/components/method-badge'
import { StateEmpty, StateError, StateLoading } from '~/shared/components/states'
import { StatusBadge } from '~/shared/components/status-badge'
import { dayjs } from '~/shared/dayjs'
import { copyText } from '~/shared/utils/clipboard'
import { humanize } from '~/shared/utils/errors'
import { isLocalAddress } from '~/shared/utils/target-check'

const PAGE_SIZES = ['20', '50', '100']
const ACTIVE_FILTER_KEYS = ['q', 'content_type', 'method', 'from', 'to'] as const

const FILTER_LABELS: Record<string, string> = {
  q: '关键字',
  content_type: 'Content-Type',
  method: '方法',
  from: '开始',
  to: '结束',
}

/** Masks the middle of the token: the URL is meant to be readable, not secret in the UI. */
function shortUrl(url: string): string {
  const idx = url.lastIndexOf('/')

  if (idx < 0 || idx + 1 >= url.length) {
    return url
  }

  const head = url.slice(0, idx + 1)
  const token = url.slice(idx + 1)

  if (token.length <= 12) {
    return url
  }

  return `${head}${token.slice(0, 6)}••••${token.slice(-4)}`
}

export function InboxScreen(): React.JSX.Element {
  const { id = '' } = useParams()
  const navigate = useNavigate()

  const [inbox, setInbox] = useState<Inbox | null>(null)
  const [items, setItems] = useState<EventListItem[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [filters, setFilters] = useState<EventFilters>({})
  const [draft, setDraft] = useState<EventFilters>({})
  const [rawKeyword, setRawKeyword] = useState('')
  const [keyword] = useDebouncedValue(rawKeyword, 300)

  const [filtersOpened, { toggle: toggleFilters }] = useDisclosure(false)
  const [clearOpened, { open: openClear, close: closeClear }] = useDisclosure(false)
  const [deleteOpened, { open: openDelete, close: closeDelete }] = useDisclosure(false)
  const [sending, setSending] = useState(false)
  const [clearing, setClearing] = useState(false)

  const [fresh, setFresh] = useState<string[]>([])
  const [pending, setPending] = useState<string[]>([])
  const [connection, setConnection] = useState<ConnectionState>('connecting')
  const [follow, setFollow] = useState(true)

  const freshTimer = useRef<number | null>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  // Same muscle memory as the inbox list: "/" focuses the keyword box.
  useHotkeys([['/', () => searchRef.current?.focus()]])

  useEffect(() => {
    void api
      .getInbox(id)
      .then(setInbox)
      .catch((err: unknown) => setError(humanize(err)))
  }, [id])

  const load = useCallback(async () => {
    setLoading(true)

    try {
      const res = await api.listEvents(id, {
        ...filters,
        limit: pageSize,
        offset: (page - 1) * pageSize,
      })

      setItems(res.items)
      setTotal(res.total)
      setError(null)
    } catch (err) {
      setError(humanize(err))
    } finally {
      setLoading(false)
    }
  }, [id, filters, page, pageSize])

  useEffect(() => {
    void load()
  }, [load])

  // The keyword applies itself (debounced); the other filters wait for "apply".
  useEffect(() => {
    setPage(1)
    setFilters((prev) => ({ ...prev, q: keyword || undefined }))
  }, [keyword])

  useEffect(() => {
    return subscribe(id, {
      onStatus: setConnection,
      onCreate: (msg) => {
        if (msg.action !== 'create') {
          return
        }

        if (follow && page === 1) {
          void load()

          setFresh((prev) => [msg.event.id, ...prev])

          if (freshTimer.current) {
            window.clearTimeout(freshTimer.current)
          }

          freshTimer.current = window.setTimeout(() => setFresh([]), 8000)
        } else {
          // The user is paging through history: do not yank the list away, just tell them.
          setPending((prev) => [msg.event.id, ...prev])
        }
      },
    })
  }, [id, load, follow, page])

  useEffect(() => {
    return () => {
      if (freshTimer.current) {
        window.clearTimeout(freshTimer.current)
      }
    }
  }, [])

  const activeFilters = useMemo(
    () =>
      ACTIVE_FILTER_KEYS.filter((key) => {
        const value = filters[key as keyof EventFilters]

        return typeof value === 'string' && value.length > 0
      }),
    [filters],
  )

  const applyFilters = () => {
    setPage(1)
    setFilters({ ...draft, q: keyword || undefined })
  }

  const resetFilters = () => {
    setDraft({})
    setRawKeyword('')
    setPage(1)
    setFilters({})
  }

  const removeFilter = (key: string) => {
    if (key === 'q') {
      setRawKeyword('')
    }

    setDraft((prev) => ({ ...prev, [key]: undefined }))
    setPage(1)
    setFilters((prev) => ({ ...prev, [key]: undefined }))
  }

  const clearEvents = async () => {
    setClearing(true)

    try {
      const res = await api.clearEvents(id)

      notifications.show({ color: 'green', message: `已清空 ${res.deleted} 条事件` })
      closeClear()
      void load()
    } catch (err) {
      notifications.show({ color: 'red', message: humanize(err) })
    } finally {
      setClearing(false)
    }
  }

  // The inbox list page has this in its row menu, but this screen is where a user
  // actually is when they decide an inbox is garbage - an entry point here is not a
  // nice-to-have. (A long junk name created by a script was literally unremovable from
  // the page the user was looking at.)
  // Sending a request by hand is the one step of the demo that needs a terminal. It does
  // not have to: the capture endpoint is public, so the page can just call it - which is
  // also the fastest way to see the live WebSocket path working.
  const sendTestRequest = async () => {
    if (!inbox) {
      return
    }

    setSending(true)

    try {
      const res = await fetch(inbox.receive_url, {
        method: 'POST',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({
          message: '这是一条测试请求',
          sent_from: 'webhook-zq 界面',
          at: new Date().toISOString(),
        }),
      })

      if (!res.ok) {
        // Read the body instead of throwing a bare status code: the server names the
        // reason ("inbox_disabled") and errors.ts already carries the Chinese wording
        // for it. "HTTP 403" tells the user nothing they can act on.
        const body = (await res.json().catch(() => null)) as
          | { error?: { code?: string; message?: string } }
          | null

        throw new ApiError(
          res.status,
          body?.error?.code ?? 'unknown',
          body?.error?.message ?? '请求失败',
        )
      }

      notifications.show({ color: 'green', message: '已发送一条测试请求' })
      void load()
    } catch (err: unknown) {
      notifications.show({ color: 'red', message: humanize(err) })
    } finally {
      setSending(false)
    }
  }

  const removeInbox = async () => {
    try {
      await api.deleteInbox(id)

      notifications.show({ message: '收件箱已删除' })
      navigate('/')
    } catch (err: unknown) {
      notifications.show({ color: 'red', message: humanize(err) })
    }
  }

  if (error) {
    return <StateError message={error} onRetry={() => void load()} />
  }

  return (
    <Stack gap="md" className="whq-enter">
      <Group justify="space-between" align="flex-start">
        <Stack gap={2}>
          <Group gap="xs">
            <Anchor component={Link} to="/" fz="sm" c="dimmed">
              <IconArrowLeft size={14} /> 收件箱
            </Anchor>
          </Group>

          <Group gap="xs">
            {/* lineClamp: a 120-character name used to push the whole header wide. */}
            <Title order={4} lineClamp={2} style={{ wordBreak: 'break-all' }}>
              {inbox?.name ?? '…'}
            </Title>

            {inbox && !inbox.enabled && (
              <StatusBadge color="gray.7">已停用</StatusBadge>
            )}
          </Group>
        </Stack>

        <Group gap="xs">
          <Tooltip label={connection === 'open' ? '实时连接正常' : '实时连接已断开，正在重连'}>
            {/* tabIndex: a tooltip on an element that cannot take focus is unreachable
                by keyboard and on touch devices. */}
            <StatusBadge
              color={connection === 'open' ? 'success.8' : 'gray.8'}
              tabIndex={0}
              aria-label={connection === 'open' ? '实时连接正常' : '实时连接已断开，正在重连'}
            >
              <span className={connection === 'open' ? undefined : 'whq-pulse'}>
                {connection === 'open' ? '● 实时' : '● 重连中'}
              </span>
            </StatusBadge>
          </Tooltip>

          <Switch
            size="xs"
            checked={follow}
            onChange={(e) => setFollow(e.currentTarget.checked)}
            label="自动合并新事件"
          />

          <Button
            size="xs"
            variant="subtle"
            color="red"
            leftSection={<IconTrash size={14} />}
            onClick={openClear}
          >
            清空事件
          </Button>

          <Menu position="bottom-end">
            <Menu.Target>
              <ActionIcon size="sm" variant="subtle" aria-label="收件箱操作">
                <IconDots size={16} />
              </ActionIcon>
            </Menu.Target>

            <Menu.Dropdown>
              <Menu.Item
                color="red"
                leftSection={<IconTrash size={14} />}
                onClick={openDelete}
              >
                删除收件箱…
              </Menu.Item>
            </Menu.Dropdown>
          </Menu>
        </Group>
      </Group>

      {inbox && (
        <Card withBorder>
          <Group gap="xs" wrap="nowrap">
            <IconTerminal2 size={15} />

            <Text fz="xs" className="whq-mono" truncate style={{ flex: 1 }}>
              {inbox.receive_url}
            </Text>

            <CopyIconButton value={inbox.receive_url} label="复制接收地址" />

            <Tooltip label="往这个地址发一条测试请求（不用敲 curl 也能看到效果）">
              <ActionIcon
                size="sm"
                variant="subtle"
                aria-label="发送测试请求"
                loading={sending}
                onClick={() => void sendTestRequest()}
              >
                <IconSend size={15} />
              </ActionIcon>
            </Tooltip>

            <Menu position="bottom-end">
              <Menu.Target>
                <ActionIcon size="sm" variant="subtle" aria-label="更多操作">
                  <IconDots size={14} />
                </ActionIcon>
              </Menu.Target>

              <Menu.Dropdown>
                <Menu.Item
                  leftSection={<IconTerminal2 size={14} />}
                  onClick={() => {
                    void (async () => {
                      try {
                        const curl = await api.exportCurl(items[0]?.id ?? '')

                        notifications.show(
                          (await copyText(curl))
                            ? { message: '已复制 cURL 命令' }
                            : { color: 'yellow', message: '无法自动复制，请在弹出的窗口里手动复制' },
                        )
                      } catch (err) {
                        notifications.show({ color: 'red', message: humanize(err) })
                      }
                    })()
                  }}
                >
                  复制最新一条的 cURL
                </Menu.Item>
              </Menu.Dropdown>
            </Menu>
          </Group>

          {/* The most common reason "nothing arrives": the address is not reachable from
              the internet, and until now nothing told the user that. */}
          {inbox && isLocalAddress(inbox.receive_url) && (
            <Text fz="xs" c="dimmed">
              第三方必须能访问到这个地址。localhost / 内网地址只有你本机能访问，需要内网穿透或公网域名。
            </Text>
          )}
        </Card>
      )}

      <Group gap="xs" align="center">
        <TextInput
          ref={searchRef}
          placeholder="搜索请求体关键字（/ 聚焦）"
          value={rawKeyword}
          onChange={(e) => setRawKeyword(e.currentTarget.value)}
          style={{ flex: 1, maxWidth: 320 }}
          size="xs"
          rightSection={
            rawKeyword ? (
              <ActionIcon size="xs" variant="subtle" onClick={() => setRawKeyword('')} aria-label="清除关键字">
                <IconX size={12} />
              </ActionIcon>
            ) : undefined
          }
        />

        <Button
          size="xs"
          variant="default"
          leftSection={<IconFilter size={13} />}
          rightSection={
            activeFilters.length > 0 ? <Badge size="xs" circle>{activeFilters.length}</Badge> : undefined
          }
          onClick={toggleFilters}
        >
          筛选
        </Button>

        {activeFilters.length > 0 && (
          <>
            {activeFilters.map((key) => (
              <Badge
                key={key}
                variant="default"
                size="sm"
                pr={4}
                rightSection={
                  <ActionIcon
                    size="xs"
                    variant="subtle"
                    onClick={() => removeFilter(key)}
                    aria-label={`移除${FILTER_LABELS[key] ?? key}筛选`}
                  >
                    <IconX size={10} />
                  </ActionIcon>
                }
              >
                {FILTER_LABELS[key] ?? key}
              </Badge>
            ))}

            <Button size="xs" variant="subtle" color="gray" onClick={resetFilters}>
              重置
            </Button>
          </>
        )}

        {/* The same count sits under the table next to the pager; on a narrow screen
            this one just wraps onto a line of its own. */}
        <Text fz="xs" c="dimmed" ml="auto" visibleFrom="sm">
          共 {total} 条
        </Text>
      </Group>

      <Collapse in={filtersOpened}>
        <Card withBorder padding="md" mb="md">
          <SimpleGrid cols={{ base: 1, sm: 2, lg: 4 }}>
            <TextInput
              label="Content-Type"
              placeholder="application/json"
              size="xs"
              value={draft.content_type ?? ''}
              onChange={(e) => setDraft((p) => ({ ...p, content_type: e.currentTarget.value }))}
            />

            <Select
              label="方法"
              placeholder="全部"
              clearable
              size="xs"
              data={['GET', 'POST', 'PUT', 'PATCH', 'DELETE']}
              value={draft.method ?? null}
              onChange={(v) => setDraft((p) => ({ ...p, method: v ?? undefined }))}
            />

            <TextInput
              label="开始时间"
              type="datetime-local"
              size="xs"
              value={draft.from ?? ''}
              onChange={(e) => setDraft((p) => ({ ...p, from: e.currentTarget.value }))}
            />

            <TextInput
              label="结束时间"
              type="datetime-local"
              size="xs"
              value={draft.to ?? ''}
              onChange={(e) => setDraft((p) => ({ ...p, to: e.currentTarget.value }))}
            />
          </SimpleGrid>

          <Group justify="flex-end" mt="md">
            <Button size="xs" variant="default" onClick={resetFilters}>
              重置
            </Button>

            <Button size="xs" onClick={applyFilters}>
              应用筛选
            </Button>
          </Group>
        </Card>
      </Collapse>

      {pending.length > 0 && (
        <Button
          size="xs"
          variant="light"
          onClick={() => {
            setPending([])
            setPage(1)
            void load()
          }}
        >
          {pending.length} 条新事件 · 点击查看
        </Button>
      )}

      {loading && items.length === 0 ? (
        <StateLoading rows={6} />
      ) : items.length === 0 ? (
        activeFilters.length > 0 ? (
          <StateEmpty
            title="没有匹配的事件"
            desc="当前筛选条件下没有任何事件。可以放宽条件，或清除筛选后重试。"
            action={
              <Button size="xs" variant="light" onClick={resetFilters}>
                清除筛选
              </Button>
            }
          />
        ) : inbox && !inbox.enabled ? (
          <StateEmpty
            title="这个收件箱已停用"
            desc="停用的收件箱会拒绝所有请求（第三方会收到 403）。启用后即可继续接收。"
          />
        ) : (
          <StateEmpty
            title="正在监听这个地址"
            // A stranger usually has no third party configured yet, so offer the one thing
            // they can do right now - send a request themselves.
            desc="向下面这个地址发一条请求，它会实时出现在这里——不需要刷新页面。还没有配置第三方？点下面的按钮复制一条 cURL，在自己的终端里发一条试试。"
            hint={
              <span className="whq-mono">
                <span className="whq-pulse">●</span> {inbox ? shortUrl(inbox.receive_url) : ''}
              </span>
            }
            action={
              inbox && (
                <CopyTextButton
                  value={`curl -X POST '${inbox.receive_url}' -H 'content-type: application/json' -d '{"hello":"world"}'`}
                  label="复制一条 cURL 试试"
                  copiedLabel="已复制 cURL"
                />
              )
            }
          />
        )
      ) : (
        <Stack gap="sm">
          <div style={{ opacity: loading ? 0.55 : 1, transition: 'opacity .2s' }}>
            {/* Six columns do not fit on a phone: scroll the table, do not squeeze it. */}
            <Table.ScrollContainer minWidth={760}>
              <Table>
                <Table.Thead>
                <Table.Tr>
                  <Table.Th>时间</Table.Th>
                  <Table.Th>方法</Table.Th>
                  <Table.Th>内容</Table.Th>
                  <Table.Th>大小</Table.Th>
                  <Table.Th>重放</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>

              <Table.Tbody>
                {items.map((ev) => (
                  <Table.Tr
                    key={ev.id}
                    className={fresh.includes(ev.id) ? 'whq-row-fresh' : undefined}
                    style={{ cursor: 'pointer' }}
                    onClick={() => navigate(`/inboxes/${id}/events/${ev.id}`)}
                  >
                    <Table.Td>
                      {/* Absolute, not "3 分钟前": the point of this list is matching an
                          event against a third party's log, and a relative time cannot be
                          read off a screenshot, a printed page or by a screen reader. */}
                      <Text fz="xs" className="whq-mono" style={{ whiteSpace: 'nowrap' }}>
                        {dayjs(ev.created_at).format('MM-DD HH:mm:ss')}
                      </Text>
                    </Table.Td>

                    <Table.Td>
                      <MethodBadge method={ev.method} />
                    </Table.Td>

                    <Table.Td>
                      <Stack gap={0}>
                        <Anchor
                          component={Link}
                          to={`/inboxes/${id}/events/${ev.id}`}
                          fz="xs"
                          lineClamp={1}
                          onClick={(e) => e.stopPropagation()}
                        >
                          {ev.preview || `(${formatBytes(ev.body_size)})`}
                        </Anchor>

                        <Text fz="xs" c="dimmed" truncate maw={420}>
                          {ev.content_type || '无 Content-Type'} · 来源 {ev.client_ip || '—'}
                        </Text>
                      </Stack>
                    </Table.Td>

                    <Table.Td>
                      <Text fz="xs" className="whq-mono">
                        {formatBytes(ev.body_size)}
                      </Text>
                    </Table.Td>

                    <Table.Td>
                      {ev.replay_count === 0 ? (
                        <Text fz="xs" c="dimmed">
                          —
                        </Text>
                      ) : (
                        <Group gap={4} wrap="nowrap">
                          <OutcomeBadge outcome={ev.last_outcome ?? ''} statusCode={ev.last_status_code} />

                          <Text fz="xs" c="dimmed">
                            ×{ev.replay_count}
                          </Text>
                        </Group>
                      )}
                    </Table.Td>

                    <Table.Td>
                      <Menu position="bottom-end">
                        <Menu.Target>
                          <ActionIcon
                            size="sm"
                            variant="subtle"
                            aria-label="事件操作"
                            onClick={(e) => e.stopPropagation()}
                          >
                            <IconDots size={14} />
                          </ActionIcon>
                        </Menu.Target>

                        <Menu.Dropdown>
                          <Menu.Item
                            leftSection={<IconTerminal2 size={14} />}
                            onClick={async (e) => {
                              e.stopPropagation()

                              try {
                                const curl = await api.exportCurl(ev.id)

                                // copyText falls back to a manual copy dialog: a bare
                                // navigator.clipboard call silently does nothing outside
                                // https/localhost, which is exactly where a demo runs.
                                notifications.show(
                                  (await copyText(curl))
                                    ? { message: '已复制 cURL 命令' }
                                    : { color: 'yellow', message: '无法自动复制，请在弹出的窗口里手动复制' },
                                )
                              } catch (err) {
                                notifications.show({ color: 'red', message: humanize(err) })
                              }
                            }}
                          >
                            复制为 cURL
                          </Menu.Item>

                          <Menu.Item
                            color="red"
                            leftSection={<IconTrash size={14} />}
                            onClick={async (e) => {
                              e.stopPropagation()

                              try {
                                await api.deleteEvent(ev.id)

                                notifications.show({ message: '已删除该事件' })
                                void load()
                              } catch (err: unknown) {
                                notifications.show({ color: 'red', message: humanize(err) })
                              }
                            }}
                          >
                            删除事件
                          </Menu.Item>
                        </Menu.Dropdown>
                      </Menu>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
              </Table>
            </Table.ScrollContainer>
          </div>

          <Group justify="space-between">
            <Group gap="xs">
              <Text fz="xs" c="dimmed">
                共 {total} 条
              </Text>

              <Select
                size="xs"
                w={92}
                data={PAGE_SIZES}
                value={String(pageSize)}
                onChange={(v) => {
                  setPageSize(Number(v ?? 20))
                  setPage(1)
                }}
              />
            </Group>

            {/* Nothing to page through when everything fits on one page. */}
            {total > pageSize && (
              <Pagination value={page} onChange={setPage} total={Math.ceil(total / pageSize)} />
            )}
          </Group>
        </Stack>
      )}

      <Modal opened={deleteOpened} onClose={closeDelete} title="删除收件箱" size="sm">
        <Stack>
          <Text fz="sm">
            将删除「{inbox?.name}」及其全部事件与重放历史，此操作不可恢复。
          </Text>

          <Group justify="flex-end">
            <Button size="xs" variant="default" onClick={closeDelete}>
              取消
            </Button>

            <Button size="xs" color="red" onClick={() => void removeInbox()}>
              删除
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={clearOpened} onClose={closeClear} title="清空事件" size="sm">
        <Stack>
          <Text fz="sm">
            将删除这个收件箱下的全部 {total} 条事件及其重放历史，此操作不可恢复。
          </Text>

          <Group justify="flex-end">
            <Button size="xs" variant="default" onClick={closeClear}>
              取消
            </Button>

            <Button size="xs" color="red" loading={clearing} onClick={() => void clearEvents()}>
              确认清空
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}
