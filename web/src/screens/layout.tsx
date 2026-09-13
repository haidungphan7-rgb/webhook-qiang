import {
  ActionIcon,
  Anchor,
  Button,
  Container,
  Group,
  Menu,
  Modal,
  Stack,
  Table,
  Text,
  TextInput,
  Tooltip,
  useMantineColorScheme,
} from '@mantine/core'
import { useHotkeys } from '@mantine/hooks'
import {
  IconBook,
  IconBolt,
  IconHelp,
  IconKey,
  IconKeyboard,
  IconMoon,
  IconSearch,
  IconSettings,
  IconSun,
} from '@tabler/icons-react'
import React, { useCallback, useEffect, useState } from 'react'
import { Link, useNavigate, Outlet } from 'react-router-dom'
import { api, formatBytes, type Inbox, type ServerSettings } from '~/api/v1'

const SETTING_LABELS: Record<string, string> = {
  max_request_body_size: '请求体上限（字节）',
  replay_timeout_ms: '重放超时（毫秒）',
  replay_max_preview: '响应预览上限（字节）',
  replay_max_redirects: '最大重定向次数',
  replay_max_retries: '最大重试次数',
  retention_max_events: '每箱保留事件数',
  retention_max_days: '事件保留天数',
  replay_rate_limit: '重放限流（次/分钟，0 = 不限）',
  auth_enabled: '访问控制',
  encryption_enabled: '敏感头加密',
  public_url_root: '对外根地址',
  // Without these the dialog falls back to the raw key and shows English in a Chinese UI.
  sensitive_headers: '敏感头名单',
  replay_allow_hosts: '内网放行白名单',
  replay_allow_private: '允许放行内网地址',
}

/** Renders a setting value: booleans and byte counts in the UI's language, not JSON's. */
function formatSetting(key: string, value: unknown): string {
  if (typeof value === 'boolean') {
    return value ? '已启用' : '未启用'
  }

  if (key === 'max_request_body_size' || key === 'replay_max_preview') {
    return formatBytes(Number(value))
  }

  if (value === '' || value === null || value === undefined) {
    return '—'
  }

  return String(value)
}

const SHORTCUTS: Array<[string, string]> = [
  ['/', '聚焦搜索框'],
  ['n', '新建收件箱'],
  ['r', '聚焦重放目标地址'],
  ['mod + K', '打开命令面板（Windows 上是 Ctrl + K）'],
  ['?', '打开这个说明'],
  ['Esc', '关闭当前弹窗'],
]

/**
 * Application shell.
 *
 * The header is deliberately thin: a brand link, a command palette entry point, a help
 * menu and the theme switch. Anything else would compete with the data on the page - this
 * is a tool, not a marketing site.
 */
export default function Layout(): React.JSX.Element {
  const navigate = useNavigate()
  const { colorScheme, setColorScheme } = useMantineColorScheme()

  const [paletteOpened, setPaletteOpened] = useState(false)
  const [query, setQuery] = useState('')
  const [inboxes, setInboxes] = useState<Inbox[]>([])

  const [settings, setSettings] = useState<ServerSettings | null>(null)
  const [settingsOpened, setSettingsOpened] = useState(false)
  const [shortcutsOpened, setShortcutsOpened] = useState(false)
  const [keyOpened, setKeyOpened] = useState(false)
  const [keyValue, setKeyValue] = useState('')

  // Whether the instance is protected decides if there is a key to enter at all.
  useEffect(() => {
    void api
      .settings()
      .then(setSettings)
      .catch(() => setSettings(null))
  }, [])

  const currentKey = (() => {
    const match = document.cookie.match(/(?:^|;\s*)whq_token=([^;]*)/)

    return match ? decodeURIComponent(match[1]) : ''
  })()

  const saveKey = () => {
    const value = keyValue.trim()

    // The server reads this cookie (see httpapi presentedToken), so it is the only way
    // to use a protected instance straight from the browser.
    document.cookie = value
      ? `whq_token=${encodeURIComponent(value)}; path=/; max-age=86400; SameSite=Lax`
      : 'whq_token=; path=/; max-age=0; SameSite=Lax'

    setKeyOpened(false)
    window.location.reload()
  }

  useHotkeys([
    ['mod+K', () => setPaletteOpened(true)],
    // "?" opens the shortcut sheet - without it the keys below are undiscoverable.
    ['?', () => setShortcutsOpened(true)],
  ])

  const openPalette = useCallback(async () => {
    setPaletteOpened(true)

    try {
      const res = await api.listInboxes({ limit: 50 })
      setInboxes(res.items)
    } catch {
      /* the palette simply stays empty */
    }
  }, [])

  useEffect(() => {
    if (paletteOpened && inboxes.length === 0) {
      void api
        .listInboxes({ limit: 50 })
        .then((res) => setInboxes(res.items))
        .catch(() => undefined)
    }
  }, [paletteOpened, inboxes.length])

  const filtered = query
    ? inboxes.filter((i) => i.name.toLowerCase().includes(query.toLowerCase()))
    : inboxes

  /** Closes the palette and jumps - shared by the click and the Enter key. */
  const goToInbox = (inboxId: string) => {
    setPaletteOpened(false)
    setQuery('')
    navigate(`/inboxes/${inboxId}`)
  }

  return (
    <>
      <a href="#main" className="visually-hidden">
        跳到主要内容
      </a>

      <Stack gap={0}>
        {/* Sticky so the palette, the theme switch and the help menu stay reachable while
            scrolling through a long payload. */}
        <Group
          h={56}
          px="md"
          justify="space-between"
          pos="sticky"
          top={0}
          bg="var(--mantine-color-body)"
          style={{ zIndex: 100, borderBottom: '1px solid var(--mantine-color-default-border)' }}
        >
          <Anchor component={Link} to="/" underline="never" c="inherit">
            <Group gap="xs">
              <IconBolt size={18} />

              <Text fw={700}>Webhook 重放平台</Text>
            </Group>
          </Anchor>

          <Group gap={4}>
            <Tooltip label="搜索收件箱（⌘K）">
              <ActionIcon variant="subtle" onClick={() => void openPalette()} aria-label="搜索收件箱">
                <IconSearch size={16} />
              </ActionIcon>
            </Tooltip>

            <Menu position="bottom-end">
              <Menu.Target>
                <ActionIcon variant="subtle" aria-label="帮助与设置">
                  <IconHelp size={16} />
                </ActionIcon>
              </Menu.Target>

              <Menu.Dropdown>
                {/* The guide first: someone who just arrived needs "what is this", not
                    the server's tuning parameters. */}
                <Menu.Item leftSection={<IconBook size={14} />} onClick={() => navigate('/help')}>
                  使用说明
                </Menu.Item>

                <Menu.Item
                  leftSection={<IconSettings size={14} />}
                  onClick={() => {
                    void api
                      .settings()
                      .then(setSettings)
                      .catch(() => setSettings(null))

                    setSettingsOpened(true)
                  }}
                >
                  服务器设置
                </Menu.Item>

                <Menu.Item
                  leftSection={<IconSearch size={14} />}
                  onClick={() => void openPalette()}
                >
                  搜索收件箱（⌘/Ctrl + K）
                </Menu.Item>

                <Menu.Item
                  leftSection={<IconKeyboard size={14} />}
                  onClick={() => setShortcutsOpened(true)}
                >
                  键盘快捷键
                </Menu.Item>

                {/* Only on a protected instance: without access control there is no key,
                    and offering the field would just be confusing. */}
                {settings?.auth_enabled && (
                  <Menu.Item
                    leftSection={<IconKey size={14} />}
                    onClick={() => {
                      setKeyValue(currentKey)
                      setKeyOpened(true)
                    }}
                  >
                    访问密钥{currentKey === '' ? '' : '（已设置）'}
                  </Menu.Item>
                )}
              </Menu.Dropdown>
            </Menu>

            <Tooltip label={colorScheme === 'dark' ? '切换到浅色' : '切换到深色'}>
              <ActionIcon
                variant="subtle"
                onClick={() => setColorScheme(colorScheme === 'dark' ? 'light' : 'dark')}
                aria-label="切换主题"
              >
                {colorScheme === 'dark' ? <IconSun size={16} /> : <IconMoon size={16} />}
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>

        {/* tabIndex makes the skip link actually move focus, not just scroll. */}
        <Container id="main" tabIndex={-1} size={1440} py="md" px={{ base: 'sm', md: 'md' }} style={{ flex: 1 }}>
          <Outlet />
        </Container>
      </Stack>

      <Modal
        opened={paletteOpened}
        onClose={() => {
          setPaletteOpened(false)
          setQuery('')
        }}
        title="跳转到收件箱"
        size="md"
      >
        <Stack gap="xs">
          <TextInput
            placeholder="输入名称过滤"
            aria-label="输入名称过滤"
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            // Enter jumps to the first match: without it the palette is a mouse only
            // feature, in an app whose whole point is that it is fast to drive.
            onKeyDown={(e) => {
              if (e.key === 'Enter' && filtered.length > 0) {
                e.preventDefault()
                goToInbox(filtered[0].id)
              }
            }}
            data-autofocus
          />

          {filtered.length === 0 ? (
            <Text fz="sm" c="dimmed" py="md">
              没有匹配的收件箱。
            </Text>
          ) : (
            // Buttons, not table rows: a row with onClick can only be reached with a
            // mouse, and this palette is the one place where keyboard speed matters.
            <Stack gap={4}>
              {filtered.slice(0, 10).map((inbox) => (
                <Button
                  key={inbox.id}
                  // A row in a list, not the page CTA: `xs` (see the size rule in theme.ts).
                  size="xs"
                  variant="subtle"
                  fullWidth
                  justify="flex-start"
                  onClick={() => goToInbox(inbox.id)}
                >
                  <Group gap="xs" justify="space-between" w="100%" wrap="nowrap">
                    <Text fz="sm" truncate>
                      {inbox.name}
                    </Text>

                    <Text fz="xs" c="dimmed" className="whq-mono">
                      {inbox.event_count} 条事件
                    </Text>
                  </Group>
                </Button>
              ))}
            </Stack>
          )}
        </Stack>
      </Modal>

      <Modal opened={settingsOpened} onClose={() => setSettingsOpened(false)} title="服务器设置" size="md">
        {settings ? (
          <Table>
            <Table.Tbody>
              {Object.entries(settings).map(([key, value]) => (
                <Table.Tr key={key}>
                  <Table.Td>
                    <Text fz="xs">{SETTING_LABELS[key] ?? key}</Text>
                  </Table.Td>

                  <Table.Td>
                    <Text fz="xs" className="whq-mono">
                      {formatSetting(key, value)}
                    </Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        ) : (
          <Text fz="sm" c="dimmed">
            无法读取服务器设置。
          </Text>
        )}

        <Text fz="xs" c="dimmed" mt="md">
          这些值由服务端启动参数决定，用于解释重放的超时、重试与被拦截行为。
        </Text>
      </Modal>

      <Modal
        opened={shortcutsOpened}
        onClose={() => setShortcutsOpened(false)}
        title="键盘快捷键"
        size="sm"
      >
        <Table>
          <Table.Tbody>
            {SHORTCUTS.map(([keys, description]) => (
              <Table.Tr key={keys}>
                <Table.Td>
                  <Text fz="xs" className="whq-mono">
                    {keys}
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">{description}</Text>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Modal>

      <Modal opened={keyOpened} onClose={() => setKeyOpened(false)} title="访问密钥" size="sm">
        <Stack gap="xs">
          <Text fz="xs" c="dimmed">
            这台服务启用了访问控制。填入你的密钥（<span className="whq-mono">--auth-keys</span> 里的
            <span className="whq-mono"> alice:keyA</span> 中冒号后面那一段），它会被存进浏览器的
            <span className="whq-mono"> whq_token</span> Cookie，服务端据此判断你是谁。留空则退出登录。
          </Text>

          <TextInput
            placeholder="例如 keyA"
            value={keyValue}
            onChange={(e) => setKeyValue(e.currentTarget.value)}
            data-autofocus
            aria-label="访问密钥"
          />

          <Group justify="flex-end">
            <Button size="xs" variant="default" onClick={() => setKeyOpened(false)}>
              取消
            </Button>

            <Button size="xs" onClick={saveKey}>
              保存并刷新
            </Button>
          </Group>
        </Stack>
      </Modal>
    </>
  )
}
