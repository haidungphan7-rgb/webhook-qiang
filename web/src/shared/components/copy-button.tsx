import type React from 'react'
import { ActionIcon, Button, Modal, Stack, Text, Textarea, Tooltip } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { IconCheck, IconCopy } from '@tabler/icons-react'
import { useEffect, useRef, useState } from 'react'
import { copyText } from '~/shared/utils/clipboard'

/**
 * A copy control that never fails silently.
 *
 * When the browser blocks programmatic copying (non-secure context), it opens a modal
 * with the text pre-selected instead of pretending the copy worked.
 */
export function CopyIconButton({
  value,
  label = '复制',
  variant = 'subtle',
}: {
  value: string
  label?: string
  variant?: string
}): React.JSX.Element {
  const [copied, setCopied] = useState(false)
  const [manual, manualHandlers] = useDisclosure(false)

  useEffect(() => {
    if (!copied) {
      return
    }

    const t = window.setTimeout(() => setCopied(false), 1500)

    return () => window.clearTimeout(t)
  }, [copied])

  const run = async () => {
    if (await copyText(value)) {
      setCopied(true)
    } else {
      manualHandlers.open()
    }
  }

  return (
    <>
      <Tooltip label={copied ? '已复制' : label}>
        <ActionIcon variant={variant} onClick={() => void run()} aria-label={label}>
          {copied ? <IconCheck size={12} /> : <IconCopy size={12} />}
        </ActionIcon>
      </Tooltip>

      <ManualCopyModal opened={manual} onClose={manualHandlers.close} value={value} />
    </>
  )
}

/** Same behaviour, but as a labelled button (used where an icon alone is too subtle). */
export function CopyTextButton({
  value,
  label = '复制',
  copiedLabel = '已复制',
  size = 'xs',
}: {
  value: string
  label?: string
  copiedLabel?: string
  size?: string
}): React.JSX.Element {
  const [copied, setCopied] = useState(false)
  const [manual, manualHandlers] = useDisclosure(false)

  const run = async () => {
    if (await copyText(value)) {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } else {
      manualHandlers.open()
    }
  }

  return (
    <>
      <Button size={size} variant="light" leftSection={<IconCopy size={13} />} onClick={() => void run()}>
        {copied ? copiedLabel : label}
      </Button>

      <ManualCopyModal opened={manual} onClose={manualHandlers.close} value={value} />
    </>
  )
}

function ManualCopyModal({
  opened,
  onClose,
  value,
}: {
  opened: boolean
  onClose: () => void
  value: string
}): React.JSX.Element {
  const ref = useRef<HTMLTextAreaElement>(null)

  return (
    <Modal opened={opened} onClose={onClose} title="请手动复制" size="lg">
      <Stack>
        <Text fz="xs" c="dimmed">
          当前环境不允许自动复制（浏览器只在 https 或 localhost 下开放剪贴板）。内容已选中，按 ⌘/Ctrl + C 即可。
        </Text>

        <Textarea
          ref={ref}
          value={value}
          readOnly
          autosize
          minRows={4}
          onFocus={(e) => e.currentTarget.select()}
          autoFocus
          styles={{ input: { fontFamily: 'var(--whq-mono)', fontSize: 12 } }}
        />

        <Button size="xs" onClick={onClose}>
          知道了
        </Button>
      </Stack>
    </Modal>
  )
}
