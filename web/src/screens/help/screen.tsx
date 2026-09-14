import { CodeHighlight } from '@mantine/code-highlight'
import {
  Accordion,
  Alert,
  Anchor,
  Badge,
  Card,
  Group,
  List,
  Stack,
  Table,
  Text,
  Title,
} from '@mantine/core'
import { IconBook, IconInfoCircle, IconKeyboard, IconShieldLock } from '@tabler/icons-react'
import React, { useEffect, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { api, formatBytes, type ServerSettings } from '~/api/v1'
import { dayjs } from '~/shared/dayjs'
import { StatusBadge } from '~/shared/components/status-badge'

const SAMPLE_CURL = `curl -X POST 'http://127.0.0.1:8080/hooks/<你的 token>' \\
  -H 'content-type: application/json' \\
  -d '{"hello":"world"}'`

/**
 * In-app usage guide.
 *
 * Every number shown here is read from the running server (`/api/v1/settings`) instead of
 * being hard coded, so the guide can never drift away from the instance the user is
 * looking at: change --replay-timeout and this page changes with it.
 */
/** The FAQ entry a deep link can open by name - keep in sync with the items below. */
const FAQ_ITEMS = ['no-event', 'blocked', 'copy', 'search', 'reveal'] as const

/** `#blocked` -> `blocked`; anything that is not a FAQ entry -> null. */
function faqFromHash(hash: string): string | null {
  const id = hash.replace('#', '')

  return id && (FAQ_ITEMS as readonly string[]).includes(id) ? id : null
}

export function HelpScreen(): React.JSX.Element {
  const [settings, setSettings] = useState<ServerSettings | null>(null)
  const { hash } = useLocation()

  /**
   * The open FAQ entry follows the URL.
   *
   * The hash is remembered alongside it because a hash-only navigation (clicking 为什么
   * while the guide is already open) does **not** remount this component: without it the
   * deep link would scroll to a panel that stays shut. Deriving it during render - rather
   * than from an effect - is both what React recommends for "state that follows a prop"
   * and what keeps the panel from flashing collapsed for one frame.
   */
  const [faq, setFaq] = useState<{ hash: string; value: string | null }>(() => ({
    hash,
    value: faqFromHash(hash),
  }))

  if (faq.hash !== hash) {
    setFaq({ hash, value: faqFromHash(hash) })
  }

  const openFaq = faq.value

  useEffect(() => {
    void api
      .settings()
      .then(setSettings)
      .catch(() => setSettings(null))
  }, [])

  /**
   * Deep links (`/help#blocked`) have to land on the answer, not above it.
   *
   * A link into a collapsed accordion is a link to nothing - the reader arrives, sees the
   * question they were sent for, and still has to find and click it. The entry is already
   * open by the time this runs (see openFaq above), so this only has to scroll to it.
   */
  useEffect(() => {
    const id = hash.replace('#', '')

    if (!id) {
      return
    }

    // One frame for the panel to exist before it can be scrolled to; opening an accordion
    // item is what makes its own height, and the scroll has to happen after that.
    const timer = window.setTimeout(() => {
      const el = document.getElementById(id)

      if (el) {
        const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches

        el.scrollIntoView({ behavior: reduced ? 'auto' : 'smooth', block: 'center' })
      }
    }, 60)

    return () => window.clearTimeout(timer)
  }, [hash])

  const timeoutS = settings ? Math.round(settings.replay_timeout_ms / 1000) : 10
  // Byte count included: "1 MiB" alone leaves the 1000 vs 1024 question open.
  const bodyLimit = settings
    ? `${formatBytes(settings.max_request_body_size)}（${settings.max_request_body_size} 字节）`
    : '1 MiB（1048576 字节）'
  const preview = settings ? formatBytes(settings.replay_max_preview) : '4 KiB'
  const rateLimit =
    settings === null ? '30' : settings.replay_rate_limit === 0 ? '不限' : `${settings.replay_rate_limit} 次/分钟`

  return (
    <Stack gap="md">
      <Group gap="xs">
        <IconBook size={20} />

        <Title order={4}>使用说明</Title>
      </Group>

      <Alert variant="light" color="brand" icon={<IconInfoCircle size={16} />} title="这是什么">
        {/* Written for somebody who has never heard the word "webhook": the problem it
            solves first, then what a webhook is, then what "replay" means. A first time
            reader decides in the first sentence whether this is what they were looking
            for, so nothing about this tool may come before that. */}
        <Text fz="sm">
          调试回调最麻烦的是「看不见」——第三方说发了，你的服务说没收到。它把中间这一段变成可见的。
        </Text>

        <Text fz="sm" mt={4}>
          Webhook 就是「事情发生时，别人主动推给你的一个 HTTP 请求」。这里给你一个
          <Text span fw={700}>临时收件地址</Text>：把地址填给 GitHub、支付网关或机器人，
          对方推过来的请求会被<Text span fw={700}>原样录下来</Text>给你看（请求头、Query、原始请求体一字不改）。
        </Text>

        <Text fz="sm" mt={4}>
          <Text span fw={700}>重放</Text>就是把录下来的这一条再原样发一次，只是这次发给你自己的服务（可以就是本机的{' '}
          <span className="whq-mono">127.0.0.1</span>）。回调通常只有一次、也不受你控制，重放让它可重复，
          用来复现问题或验证处理逻辑。全程不用改一行业务代码。
        </Text>
      </Alert>

      <Card withBorder>
        <Stack gap="xs">
          <Title order={5}>四步上手</Title>

          <List type="ordered" size="sm" spacing="xs">
            <List.Item>
              在<Anchor component={Link} to="/">收件箱列表</Anchor>点「新建收件箱」，填个名字（比如「GitHub 推送」）。
            </List.Item>

            <List.Item>复制它的接收地址，填到第三方服务的 Webhook 配置里。</List.Item>

            <List.Item>
              触发一次真实回调；本地调试可以直接发一条（把地址换成你自己的）：
              <CodeHighlight code={SAMPLE_CURL} language="bash" withCopyButton copyLabel="复制" copiedLabel="已复制" radius="sm" mt={4} />
              成功会返回 <Text span fz="xs" className="whq-mono">{'{"id":"…","ok":true}'}</Text>，响应头里也有{' '}
              <Text span fz="xs" className="whq-mono">X-Wh-Event-Id</Text>。
            </List.Item>

            <List.Item>
              打开收件箱 → 点开那条事件 → 在右侧填<Text span fw={600}>你自己的服务地址</Text>（例如{' '}
              <span className="whq-mono">http://127.0.0.1:3000/webhook</span>）→ 点「重放」。
              新事件不用刷新页面就会自己出现。
            </List.Item>
          </List>
        </Stack>
      </Card>

      {/* `#replay` is where the event screen's "完整规则" link lands. */}
      <Card withBorder id="replay">
        <Stack gap="xs">
          <Group gap="xs">
            <IconShieldLock size={16} />

            <Title order={5}>重放的规则</Title>
          </Group>

          <Table withTableBorder>
            <Table.Tbody>
              <Table.Tr>
                <Table.Td w={{ base: 96, sm: 180 }}>
                  <Text fz="xs" fw={600}>
                    会转发
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">
                    原始请求体（字节不变）、Content-Type，以及除下面两类之外的请求头。
                  </Text>
                </Table.Td>
              </Table.Tr>

              <Table.Tr>
                <Table.Td>
                  <Text fz="xs" fw={600}>
                    不会转发
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">
                    Host、Content-Length、Authorization、Cookie、X-API-Key 等凭据类请求头，以及逐跳头（Connection、
                    X-Forwarded-*）。
                  </Text>
                </Table.Td>
              </Table.Tr>

              <Table.Tr>
                <Table.Td>
                  <Text fz="xs" fw={600}>
                    目标地址限制
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">
                    只允许 http/https，URL 里不能带用户名密码。内网、本机、链路本地（含 169.254.169.254
                    云元数据）、保留地址一律拒绝。
                    <Text span fw={700}>
                      被拒绝的请求不会发出，但会记一条「被拦截」，可以在重放历史里查到
                    </Text>
                    ：也就是说重放失败不等于对方没收到，要分清“没发出去”和“发了但失败”。
                    {/* `/settings` is an untyped map server side, so treat the list as
                        optional: one missing field must not take the whole guide down. */}
                    {(settings?.replay_allow_hosts ?? []).length > 0 && (
                      <>
                        {' '}
                        当前已配置白名单主机：
                        <span className="whq-mono">{(settings?.replay_allow_hosts ?? []).join('、')}</span>
                      </>
                    )}
                  </Text>
                </Table.Td>
              </Table.Tr>

              <Table.Tr>
                <Table.Td>
                  <Text fz="xs" fw={600}>
                    超时
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">
                    {timeoutS} 秒（服务端 replay-timeout）。超时记为「超时」，不是失败也不是成功。
                  </Text>
                </Table.Td>
              </Table.Tr>

              <Table.Tr>
                <Table.Td>
                  <Text fz="xs" fw={600}>
                    响应保存
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">只保存前 {preview} 作为预览，超出的部分截断（不会把大响应塞进数据库）。</Text>
                </Table.Td>
              </Table.Tr>

              <Table.Tr>
                <Table.Td>
                  <Text fz="xs" fw={600}>
                    重定向
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">
                    默认不跟随（把 3xx 原样记下来）。即使开启，每一跳都会重新做地址检查，跳到内网一样被拦。
                  </Text>
                </Table.Td>
              </Table.Tr>

              <Table.Tr>
                <Table.Td>
                  <Text fz="xs" fw={600}>
                    限流
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">
                    {rateLimit}（服务端 replay-rate-limit，按调用方计：配了多租户就按租户，否则按来源
                    IP）。超限返回 429，页面会给出倒计时，到点可直接重试。
                    <Text span fw={700}>被限流的这一次不会写进重放历史</Text>
                    ——它在真正发起重放之前就被拒绝了。
                  </Text>
                </Table.Td>
              </Table.Tr>

              <Table.Tr>
                <Table.Td>
                  <Text fz="xs" fw={600}>
                    成功 / 失败
                  </Text>
                </Table.Td>

                <Table.Td>
                  <Text fz="xs">
                    目标返回 2xx 才算「成功」；收到响应但不是 2xx 记为「非 2xx」，仍然会保存状态码与响应预览。
                  </Text>
                </Table.Td>
              </Table.Tr>
            </Table.Tbody>
          </Table>

          <Text fz="xs" c="dimmed">
            以上数值来自当前服务实例的 <span className="whq-mono">/api/v1/settings</span>
            {settings && ` · 读取于 ${dayjs().format('HH:mm:ss')}`}。用不同参数重启服务，这页会跟着变。
          </Text>
        </Stack>
      </Card>

      <Card withBorder>
        <Stack gap="xs">
          <Title order={5}>敏感请求头</Title>

          <Text fz="sm">
            {/* `component="span"`: the Badge root is a div, and a div inside this Text's <p>
                is invalid HTML that React reports as a hydration error. */}
            下面这些请求头默认
            <StatusBadge component="span" size="xs" color="gray.8">
              掩码显示
            </StatusBadge>
            ，而且
            <Text span fw={700}>数据库里也不存明文</Text>
            ：配置了加密密钥时以密文存储，没配置时直接存掩码（原值不可恢复）。
            详情页可以打开「显示敏感头原始值」查看，前提是服务启用了访问控制——否则接口是开放的，解密就失去意义了。
            每次查看都会记审计日志。
          </Text>

          {/* The list comes from the server so it can never contradict the binary. */}
          <Group gap={4}>
            {(settings?.sensitive_headers ?? []).map((name) => (
              <Badge key={name} size="xs" variant="default" className="whq-mono">
                {name}
              </Badge>
            ))}
          </Group>

          {settings !== null && (settings.sensitive_headers ?? []).length === 0 && (
            <Text fz="xs" c="dimmed">
              服务端未返回清单。
            </Text>
          )}
        </Stack>
      </Card>

      <Card withBorder>
        <Stack gap="xs">
          <Title order={5}>常见问题</Title>

          <Accordion variant="separated" value={openFaq} onChange={(value) => setFaq({ hash, value })}>
            <Accordion.Item value="no-event" id="no-event">
              <Accordion.Control>发了请求，页面上没有出现</Accordion.Control>

              <Accordion.Panel>
                <Stack gap={4}>
                  <Text fz="xs">
                    先看响应码：地址里的 token 不存在会得到 404，收件箱被停用会得到 403，请求体超过 {bodyLimit}{' '}
                    会得到 413 且不保存。
                  </Text>

                  <Text fz="xs">
                    确认第三方填的地址和收件箱里显示的完全一致（token 区分大小写）；也可以先用上面的 curl
                    自己发一条，排除第三方侧的问题。
                  </Text>
                </Stack>
              </Accordion.Panel>
            </Accordion.Item>

            {/* `#blocked` is where the event screen's "为什么" link lands: the reader is
                sent here after a replay was refused, so this one opens itself. */}
            <Accordion.Item value="blocked" id="blocked">
              <Accordion.Control>重放显示「被拦截」</Accordion.Control>

              <Accordion.Panel>
                <Text fz="xs">
                  目标命中了安全策略：内网 / 本机 / 保留地址，或不是 http(s)。这是为了防止服务器被当成打内网的跳板。
                  要重放到本机，需要用 <span className="whq-mono">--replay-allow-host</span> 和{' '}
                  <span className="whq-mono">--replay-allow-private</span> 重启服务。只在自己机器上这样用；
                  暴露在公网的服务不要加这两个参数——那等于允许别人借你的服务访问你的内网。
                </Text>

                <Text fz="xs" c="dimmed">
                  注：地址写得不对（比如带用户名和密码）在重放历史里也显示成「被拦截」——服务端目前不细分这两类。
                  具体原因看结果卡里的「服务端返回的原始信息」。
                </Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="copy" id="copy">
              <Accordion.Control>点复制没反应</Accordion.Control>

              <Accordion.Panel>
                <Text fz="xs">
                  浏览器只在 https 或 localhost 下允许自动写入剪贴板。如果你用局域网 IP 通过 http
                  访问，自动复制会被拒绝——此时会弹出一个窗口，内容已选中，按 ⌘/Ctrl + C 即可。
                </Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="search" id="search">
              <Accordion.Control>关键字搜不到明明存在的事件</Accordion.Control>

              <Accordion.Panel>
                <Text fz="xs">
                  只有内容是合法 UTF-8 且不含 NUL 的请求体才参与搜索（二进制 payload
                  不参与，但仍然可以查看和重放），且只索引前 64 KiB。
                </Text>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="reveal" id="reveal">
              <Accordion.Control>「显示敏感头原始值」是灰的 / 点了没变化</Accordion.Control>

              <Accordion.Panel>
                <Text fz="xs">
                  两个原因：这条事件没有敏感头时开关会禁用；服务没有启用访问控制时，接口是开放的，此时不会提供解密
                  ——需要带 <span className="whq-mono">--auth-token</span> 或{' '}
                  <span className="whq-mono">--auth-keys</span> 启动。
                </Text>
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
        </Stack>
      </Card>

      <Card withBorder>
        <Stack gap="xs">
          <Group gap="xs">
            <IconKeyboard size={16} />

            <Title order={5}>快捷键</Title>
          </Group>

          <Table withTableBorder>
            <Table.Tbody>
              {[
                ['/', '聚焦搜索框'],
                ['n', '新建收件箱'],
                ['r', '聚焦重放目标地址'],
                ['⌘/Ctrl + K', '打开命令面板，跳转到收件箱'],
                ['?', '打开快捷键说明'],
              ].map(([keys, desc]) => (
                <Table.Tr key={keys}>
                  <Table.Td w={{ base: 80, sm: 120 }}>
                    <Text fz="xs" className="whq-mono">
                      {keys}
                    </Text>
                  </Table.Td>

                  <Table.Td>
                    <Text fz="xs">{desc}</Text>
                  </Table.Td>
                </Table.Tr>
              ))}
            </Table.Tbody>
          </Table>
        </Stack>
      </Card>

      {/* "Which version are you running?" is the first question on any bug report, and it
          used to require a shell. Read from /settings, so it is the server's own answer. */}
      <Text fz="xs" c="dimmed" ta="center">
        {versionLine(settings)}
      </Text>
    </Stack>
  )
}

/**
 * The footer version line.
 *
 * `0.0.0@undefined` / `unknown` are what the binary reports when it was built without the
 * version ldflags (see internal/version), i.e. `go run`. That is not a version, so it is
 * reported as unknown rather than printed as if it were one - and the build time is only
 * shown when it parses, never reformatted into something that looks precise.
 */
function versionLine(settings: ServerSettings | null): string {
  const version = settings?.version ?? ''

  if (version === '' || version.startsWith('0.0.0@')) {
    return '版本未知：这个服务端不是打过版本的构建（go run 或未带版本信息编译），报问题时请说明你如何启动它。'
  }

  const built = dayjs(settings?.build_time)
  const when = settings?.build_time && built.isValid() ? built.format('YYYY-MM-DD HH:mm UTC') : ''

  return when === '' ? `当前版本：${version}` : `当前版本：${version}（构建于 ${when}）`
}
