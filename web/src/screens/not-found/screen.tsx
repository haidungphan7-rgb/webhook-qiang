import type React from 'react'
import { Center, Text, Button, Title, Stack } from '@mantine/core'
import { Link } from 'react-router-dom'

export function NotFoundScreen(): React.JSX.Element {
  return (
    <Center maw="100%" h="100%">
      <Stack align="center">
        <Title size="10em" style={{ fontFamily: 'monospace' }}>
          404
        </Title>

        <Stack align="center">
          <Text size="lg">页面不存在</Text>

          <Link to="/">
            {/* The only action on the page, so it is the page CTA: `sm` (36px), the same
                size as 新建收件箱 and 重放. `xs` is for inline and toolbar buttons. */}
            <Button variant="filled">
              回到收件箱
            </Button>
          </Link>
        </Stack>
      </Stack>
    </Center>
  )
}
