import { CodeHighlight } from '@mantine/code-highlight'
import {
  ActionIcon,
  Anchor,
  Button,
  Group,
  List,
  Menu,
  Modal,
  Pagination,
  SegmentedControl,
  Stack,
  Table,
  Text,
  TextInput,
  Title,
  Tooltip,
} from '@mantine/core'
import { useDebouncedValue, useDisclosure, useHotkeys } from '@mantine/hooks'
import { notifications } from '@mantine/notifications'
import {
  IconCheck,
  IconCopy,
  IconDots,
  IconPlus,
  IconRefresh,
  IconSearch,
  IconTrash,
  IconX,
} from '@tabler/icons-react'
import React, { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useNavigate } from 'react-router-dom'
import { api, type Inbox } from '~/api/v1'
import { CopyIconButton, CopyTextButton } from '~/shared/components/copy-button'
import { StateEmpty, StateError, StateLoading } from '~/shared/components/states'
import { StatusBadge } from '~/shared/components/status-badge'
import { dayjs } from '~/shared/dayjs'
import { copyText } from '~/shared/utils/clipboard'
import { humanize } from '~/shared/utils/errors'
import { isLocalAddress } from '~/shared/utils/target-check'

const PAGE_SIZE = 20

type StatusFilter = 'all' | 'enabled' | 'disabled'

/** Masks the middle of the token: readable in the UI, still not a copy-paste target. */
function shortUrl(url: string): string {
  const idx = url.lastIndexOf('/')
  if (idx < 0 || idx + 1 >= url.length) {
    return url
  }

  const token = url.slice(idx + 1)
  if (token.length <= 12) {
    return url
  }

  return `${url.slice(0, idx + 1)}${token.slice(0, 6)}••••${token.slice(-4)}`
}

function sampleCurl(url: string): string {
  return `curl -X POST '${url}' \\\n  -H 'content-type: application/json' \\\n  -d '{"hello":"world"}'`
}

function sampleJs(url: string): string {
  return `await fetch('${url}', {
  method: 'POST',
  headers: { 'content-type': 'application/json' },
  body: JSON.stringify({ hello: 'world' }),
})`
}

function samplePython(url: string): string {
  return `import requests

requests.post('${url}', json={'hello': 'world'})`
}

export function InboxesScreen(): React.JSX.Element {
  const navigate = useNavigate()

  const [items, setItems] = useState<Inbox[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [rawSearch, setRawSearch] = useState('')
  const [search] = useDebouncedValue(rawSearch, 300)
  const [status, setStatus] = useState<StatusFilter>('all')

  const [createOpened, { open: openCreate, close: closeCreate }] = useDisclosure(false)
  const [guide, setGuide] = useState<Inbox | null>(null)
  const [confirm, setConfirm] = useState<{ inbox: Inbox; action: 'delete' | 'rotate' } | null>(null)
  const [busy, setBusy] = useState(false)
  const [newToken, setNewToken] = useState<string | null>(null)

  const searchRef = useRef<HTMLInputElement>(null)

  // Keyboard first: "/" focuses search, "n" opens the create dialog.
  useHotkeys([
    ['/', () => searchRef.current?.focus()],
    ['n', () => openCreate()],
  ])

  const load = useCallback(async () => {
    setLoading(true)

    try {
      const res = await api.listInboxes({
        q: search || undefined,
        enabled: status === 'all' ? undefined : status === 'enabled',
        limit: PAGE_SIZE,
        offset: (page - 1) * PAGE_SIZE,
      })

      setItems(res.items)
      setTotal(res.total)
      setError(null)
    } catch (err) {
      setError(humanize(err))
    } finally {
      setLoading(false)
    }
  }, [page, search, status])

  useEffect(() => {
    void load()
  }, [load])

  useEffect(() => {
    setPage(1)
  }, [search, status])

  const toggleEnabled = async (inbox: Inbox) => {
    try {
      await api.patchInbox(inbox.id, { enabled: !inbox.enabled })
      void load()
    } catch (err) {
      notifications.show({ color: 'red', message: humanize(err) })
    }
  }

  const runConfirm = async () => {
    if (!confirm) {
      return
    }

    setBusy(true)

    try {
      if (confirm.action === 'delete') {
        await api.deleteInbox(confirm.inbox.id)
        notifications.show({ message: `已删除「${confirm.inbox.name}」` })
      } else {
        const res = await api.rotateToken(confirm.inbox.id)

        // Never auto-close: the new token cannot be recovered afterwards.
        setNewToken(res.token)
        notifications.show({ color: 'blue', message: '已生成新 token，请立即保存' })
      }

      setConfirm(null)
      void load()
    } catch (err) {
      notifications.show({ color: 'red', message: humanize(err) })
    } finally {
      setBusy(false)
    }
  }

  if (error) {
    return <StateError message={error} onRetry={() => void load()} />
  }

  return (
    // `whq-enter`: the one entrance animation in the app (see app.css). It is on the page
    // root so a navigation reads as a change, not as a flicker - and it is off entirely
    // when the user asked for reduced motion.
    <Stack gap="md" className="whq-enter">
      <Group justify="space-between">
        <Group gap="xs">
          <Title order={4}>收件箱</Title>

          <Text fz="xs" c="dimmed">
            共 {total} 个
          </Text>
        </Group>

        <Button leftSection={<IconPlus size={15} />} onClick={openCreate}>
          新建收件箱
        </Button>
      </Group>

      <Group gap="xs">
        <TextInput
          ref={searchRef}
          placeholder="按名称搜索（/ 聚焦）"
          leftSection={<IconSearch size={14} />}
          value={rawSearch}
          onChange={(e) => setRawSearch(e.currentTarget.value)}
          rightSection={
            rawSearch ? (
              <ActionIcon size="xs" variant="subtle" onClick={() => setRawSearch('')} aria-label="清除搜索">
                <IconX size={12} />
              </ActionIcon>
            ) : undefined
          }
          style={{ maxWidth: 280 }}
        />

        <SegmentedControl
          size="xs"
          value={status}
          onChange={(v) => setStatus(v as StatusFilter)}
          data={[
            { value: 'all', label: '全部' },
            { value: 'enabled', label: '启用' },
            { value: 'disabled', label: '停用' },
          ]}
        />
      </Group>

      {loading && items.length === 0 ? (
        <StateLoading rows={5} />
      ) : items.length === 0 ? (
        search || status !== 'all' ? (
          <StateEmpty
            title="没有匹配的收件箱"
            desc="换个关键词，或把状态切回“全部”试试。"
            action={
              <Button
                size="xs"
                variant="light"
                onClick={() => {
                  setRawSearch('')
                  setStatus('all')
                }}
              >
                清除筛选
              </Button>
            }
          />
        ) : (
          <StateEmpty
            title="还没有收件箱"
            // Written for somebody who has never used a webhook inspector: say what a
            // webhook is before saying what to click.
            desc="Webhook 是第三方（GitHub、支付网关、机器人……）在事件发生时主动推给你的 HTTP 请求。建一个收件箱，拿到一个临时接收地址，把它填给对方，请求就会实时出现在这里。"
            action={
              <Group gap="xs">
                <Button size="sm" leftSection={<IconPlus size={14} />} onClick={openCreate}>
                  创建第一个收件箱
                </Button>

                <Button size="sm" variant="default" component={Link} to="/help">
                  先看使用说明
                </Button>
              </Group>
            }
          >
            <FirstRunGuide />
          </StateEmpty>
        )
      ) : (
        <Stack gap="sm">
          <div style={{ opacity: loading ? 0.55 : 1, transition: 'opacity .2s' }}>
            {/* Six columns do not fit on a phone: scroll the table, do not squeeze it. */}
            <Table.ScrollContainer minWidth={760}>
              <Table>
                <Table.Thead>
                <Table.Tr>
                  <Table.Th>名称与地址</Table.Th>
                  <Table.Th>状态</Table.Th>
                  <Table.Th>事件</Table.Th>
                  <Table.Th>最近事件</Table.Th>
                  <Table.Th>响应码</Table.Th>
                  <Table.Th />
                </Table.Tr>
              </Table.Thead>

              <Table.Tbody>
                {items.map((inbox) => (
                  <Table.Tr
                    key={inbox.id}
                    style={{
                      cursor: 'pointer',
                      opacity: inbox.enabled ? 1 : 0.6,
                    }}
                    onClick={() => navigate(`/inboxes/${inbox.id}`)}
                  >
                    <Table.Td>
                      <Stack gap={0}>
                        <Anchor
                          component={Link}
                          to={`/inboxes/${inbox.id}`}
                          fz="sm"
                          fw={500}
                          onClick={(e) => e.stopPropagation()}
                        >
                          {inbox.name}
                        </Anchor>

                        <Group gap={4} wrap="nowrap">
                          <Text fz="xs" c="dimmed" className="whq-mono" truncate maw={260}>
                            {shortUrl(inbox.receive_url)}
                          </Text>

                          <span onClick={(e) => e.stopPropagation()}>
                            <CopyIconButton value={inbox.receive_url} label="复制接收地址" />
                          </span>
                        </Group>
                      </Stack>
                    </Table.Td>

                    <Table.Td>
                      {/* Same word and same variant in the list and on the detail page:
                          two different looks for one state reads as two states. */}
                      <StatusBadge color={inbox.enabled ? 'success.8' : 'gray.8'}>
                        {inbox.enabled ? '启用' : '已停用'}
                      </StatusBadge>
                    </Table.Td>

                    <Table.Td>
                      <Text fz="xs" className="whq-mono" fw={600}>
                        {inbox.event_count}
                      </Text>
                    </Table.Td>

                    <Table.Td>
                      {/* Absolute for the same reason as the event list: this is the time
                          you match against the other side's log. */}
                      <Text fz="xs" c="dimmed" className="whq-mono" style={{ whiteSpace: 'nowrap' }}>
                        {inbox.last_event_at ? dayjs(inbox.last_event_at).format('MM-DD HH:mm:ss') : '—'}
                      </Text>
                    </Table.Td>

                    <Table.Td>
                      <Tooltip label={`该收件箱固定返回此状态码（延迟 ${inbox.response_delay_ms}ms）`}>
                        {/* tabIndex: a tooltip on an element that cannot take focus is
                            unreachable by keyboard and on touch devices. */}
                        {/* StatusBadge, not Badge: the `default` variant ignores `color`
                            entirely, so this colour coding used to do nothing. */}
                        <StatusBadge
                          tabIndex={0}
                          aria-label={`固定返回 ${inbox.response_code}，延迟 ${inbox.response_delay_ms} 毫秒`}
                          color={
                            inbox.response_code < 300
                              ? 'success.8'
                              : inbox.response_code < 400
                                ? 'brand.8'
                                : 'error.8'
                          }
                        >
                          {inbox.response_code}
                        </StatusBadge>
                      </Tooltip>
                    </Table.Td>

                    <Table.Td onClick={(e) => e.stopPropagation()}>
                      <Menu position="bottom-end">
                        <Menu.Target>
                          <ActionIcon variant="subtle" aria-label="收件箱操作">
                            <IconDots size={15} />
                          </ActionIcon>
                        </Menu.Target>

                        <Menu.Dropdown>
                          <Menu.Item
                            leftSection={<IconCopy size={14} />}
                            onClick={() => {
                              void copyText(inbox.receive_url).then((ok) =>
                                notifications.show(
                                  ok
                                    ? { message: '已复制接收地址' }
                                    : { color: 'yellow', message: '无法自动复制，请手动选中地址复制' },
                                ),
                              )
                            }}
                          >
                            复制接收地址
                          </Menu.Item>

                          <Menu.Item
                            leftSection={inbox.enabled ? <IconX size={14} /> : <IconCheck size={14} />}
                            onClick={() => void toggleEnabled(inbox)}
                          >
                            {inbox.enabled ? '停用' : '启用'}
                          </Menu.Item>

                          <Menu.Item
                            leftSection={<IconSearch size={14} />}
                            onClick={() => setGuide(inbox)}
                          >
                            查看接入说明
                          </Menu.Item>

                          <Menu.Item
                            leftSection={<IconRefresh size={14} />}
                            onClick={() => setConfirm({ inbox, action: 'rotate' })}
                          >
                            轮换 token…
                          </Menu.Item>

                          <Menu.Item
                            color="red"
                            leftSection={<IconTrash size={14} />}
                            onClick={() => setConfirm({ inbox, action: 'delete' })}
                          >
                            删除…
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

          {/* Nothing to page through when everything fits on one page. */}
          {total > PAGE_SIZE && (
            <Pagination value={page} onChange={setPage} total={Math.ceil(total / PAGE_SIZE)} />
          )}
        </Stack>
      )}

      <CreateInboxModal
        opened={createOpened}
        onClose={closeCreate}
        onCreated={(inbox) => {
          closeCreate()
          setGuide(inbox)
          void load()
        }}
      />

      <GuideModal inbox={guide} onClose={() => setGuide(null)} />

      <Modal
        opened={confirm !== null}
        onClose={() => setConfirm(null)}
        title={confirm?.action === 'delete' ? '删除收件箱' : '轮换 token'}
        size="sm"
      >
        <Stack>
          <Text fz="sm">
            {confirm?.action === 'delete'
              ? `将删除「${confirm.inbox.name}」及其全部事件与重放历史，此操作不可恢复。`
              : `将为「${confirm?.inbox.name}」生成新的 token，旧地址立即失效，第三方需要同步修改。`}
          </Text>

          <Group justify="flex-end">
            <Button size="xs" variant="default" onClick={() => setConfirm(null)}>
              取消
            </Button>

            <Button
              size="xs"
              color={confirm?.action === 'delete' ? 'red' : 'blue'}
              loading={busy}
              onClick={() => void runConfirm()}
            >
              确认
            </Button>
          </Group>
        </Stack>
      </Modal>

      <Modal opened={newToken !== null} onClose={() => setNewToken(null)} title="新的接收 token" size="md">
        <Stack>
          <Text fz="xs" c="dimmed">
            旧 token 已失效。请立即复制并更新到第三方配置，关闭后无法再次查看。
          </Text>

          <CodeHighlight code={newToken ?? ''} language="plaintext" withCopyButton radius="sm" />

          <Group justify="flex-end">
            <Button size="xs" onClick={() => setNewToken(null)}>
              我已保存
            </Button>
          </Group>
        </Stack>
      </Modal>
    </Stack>
  )
}

/**
 * What this tool is, in three lines.
 *
 * Shown only while the list is completely empty. A first time visitor has no idea what an
 * "inbox" is here, and a bare "create one" button answers the wrong question; once there
 * is a first inbox the question is already answered, so the guide never comes back.
 */
const FIRST_RUN_STEPS: Array<[string, string]> = [
  ['建一个收件箱', '拿到一个专属的接收地址（就是一串 URL），不需要你部署任何东西。'],
  ['把地址填给第三方', '在对方的 webhook 设置里填这串 URL —— 那通常就叫「回调地址 / Webhook URL」。'],
  ['回来查看并重放', '请求会实时出现在这里；确认内容后，可以把同一条原样重放到你的服务，用来复现问题。'],
]

function FirstRunGuide(): React.JSX.Element {
  return (
    <List type="ordered" size="xs" spacing={4} c="dimmed" w="100%" maw={460} ta="left">
      {FIRST_RUN_STEPS.map(([title, detail]) => (
        <List.Item key={title}>
          <Text span fw={600}>
            {title}
          </Text>
          ：{detail}
        </List.Item>
      ))}
    </List>
  )
}

/** Short modal with a single field; submits on Enter and explains why it is disabled. */
function CreateInboxModal({
  opened,
  onClose,
  onCreated,
}: {
  opened: boolean
  onClose: () => void
  onCreated: (inbox: Inbox) => void
}): React.JSX.Element {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    if (name.trim() === '') {
      setError('请填写名称（必填，最多 120 个字符）')

      return
    }

    setBusy(true)

    try {
      onCreated(await api.createInbox(name.trim()))
      setName('')
      setError(null)
    } catch (err) {
      setError(humanize(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Modal opened={opened} onClose={onClose} title="新建收件箱" size="sm">
      <form
        onSubmit={(e) => {
          e.preventDefault()
          void submit()
        }}
      >
        <Stack>
          <TextInput
            label="名称"
            // The only field, so say what it is for: strangers pause here wondering whether
            // it has to match something on the other side.
            description="只是给你自己看的标记，方便在列表里认出它，不需要和第三方一致。"
            placeholder="例如：GitHub 推送"
            value={name}
            onChange={(e) => setName(e.currentTarget.value)}
            error={error}
            data-autofocus
          />

          <Group justify="flex-end">
            <Button size="xs" variant="default" onClick={onClose}>
              取消
            </Button>

            <Button size="xs" type="submit" loading={busy}>
              创建
            </Button>
          </Group>
        </Stack>
      </form>
    </Modal>
  )
}

/**
 * Shown right after an inbox is created (and reachable later from the row menu).
 *
 * The point is to get the user from "I have an inbox" to "I can see my first event" in
 * under a minute, so the samples are copy-paste ready in three languages.
 */
function GuideModal({ inbox, onClose }: { inbox: Inbox | null; onClose: () => void }): React.JSX.Element {
  const [lang, setLang] = useState('curl')
  const navigate = useNavigate()

  if (!inbox) {
    return <></>
  }

  const code =
    lang === 'curl'
      ? sampleCurl(inbox.receive_url)
      : lang === 'javascript'
        ? sampleJs(inbox.receive_url)
        : samplePython(inbox.receive_url)

  return (
    <Modal opened onClose={onClose} title={`接入说明 · ${inbox.name}`} size="lg">
      <Stack gap="sm">
        <Stack gap={4}>
          <Text fz="xs" c="dimmed">
            接收地址
          </Text>

          <Group gap="xs" wrap="nowrap">
            <Text fz="xs" className="whq-mono" truncate style={{ flex: 1 }}>
              {inbox.receive_url}
            </Text>

            <CopyTextButton value={inbox.receive_url} label="复制" />
          </Group>

          {/* If the address is not reachable from the internet, the whole exercise fails
              silently - say it while the user is still setting things up. */}
          {isLocalAddress(inbox.receive_url) && (
            <Text fz="xs" c="dimmed">
              第三方必须能访问到这个地址。localhost / 内网地址只有你本机能访问，需要内网穿透或公网域名。
            </Text>
          )}
        </Stack>

        <SegmentedControl
          size="xs"
          value={lang}
          onChange={setLang}
          data={[
            { value: 'curl', label: 'cURL' },
            { value: 'javascript', label: 'JavaScript' },
            { value: 'python', label: 'Python' },
          ]}
        />

        <CodeHighlight
          code={code}
          language={lang === 'curl' ? 'bash' : lang}
          withCopyButton
          copyLabel="复制"
          copiedLabel="已复制"
          radius="sm"
        />

        <Group justify="flex-end">
          <Button size="xs" variant="default" onClick={onClose}>
            完成
          </Button>

          <Button
            size="xs"
            onClick={() => {
              onClose()
              navigate(`/inboxes/${inbox.id}`)
            }}
          >
            打开收件箱，等待第一条事件
          </Button>
        </Group>
      </Stack>
    </Modal>
  )
}
