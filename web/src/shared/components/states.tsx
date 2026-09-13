import type React from 'react'
import { Alert, Button, Center, Skeleton, Stack, Text, Title } from '@mantine/core'
import { IconInbox, IconRefreshAlert } from '@tabler/icons-react'

/**
 * The three states every list and detail view can be in.
 *
 * They live here so all screens look and behave the same: the empty state explains what
 * this place is and what to do next, the error state says what happened and offers a
 * retry, and the loading state keeps the same height as the real content so the layout
 * never jumps.
 */

export function StateEmpty({
  title,
  desc,
  action,
  hint,
  icon,
  children,
}: {
  title: string
  desc: string
  action?: React.ReactNode
  hint?: React.ReactNode
  icon?: React.ReactNode
  /**
   * Optional extra content between the action and the hint - the first run guide uses it.
   * It comes after `action` on purpose: the button stays the first thing the eye lands on.
   */
  children?: React.ReactNode
}): React.JSX.Element {
  return (
    <Center
      mih={200}
      py="xl"
      style={{
        border: '1px dashed var(--mantine-color-default-border)',
        borderRadius: 'var(--mantine-radius-md)',
      }}
    >
      <Stack align="center" gap="xs" maw={560} px="md">
        <Text c="dimmed">{icon ?? <IconInbox size={22} stroke={1.5} />}</Text>

        <Title order={5}>{title}</Title>

        <Text fz="sm" c="dimmed" ta="center">
          {desc}
        </Text>

        {action}

        {children}

        {hint && (
          <Text fz="xs" c="dimmed" ta="center" mt="xs">
            {hint}
          </Text>
        )}
      </Stack>
    </Center>
  )
}

/** Error state: what happened, why, and what to do about it. */
export function StateError({
  title = '加载失败',
  message,
  onRetry,
}: {
  title?: string
  message: string
  onRetry?: () => void
}): React.JSX.Element {
  return (
    <Alert color="error" variant="light" radius="md" icon={<IconRefreshAlert size={16} />} title={title}>
      <Stack gap="xs">
        <Text fz="sm">{message}</Text>

        {onRetry && (
          <div>
            <Button size="xs" variant="light" color="error" onClick={onRetry}>
              重试
            </Button>
          </div>
        )}
      </Stack>
    </Alert>
  )
}

/** Loading placeholder that reserves the final height. */
export function StateLoading({ rows = 6 }: { rows?: number }): React.JSX.Element {
  return (
    <Stack gap="xs">
      {Array.from({ length: rows }).map((_, i) => (
        <Skeleton key={i} height={36} radius="sm" />
      ))}
    </Stack>
  )
}
