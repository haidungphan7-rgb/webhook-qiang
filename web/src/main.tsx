import type React from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider, createBrowserRouter } from 'react-router-dom'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { CodeHighlightAdapterProvider, createHighlightJsAdapter } from '@mantine/code-highlight'
import hljs from 'highlight.js/lib/core'
import { routes } from '~/routing'
import { theme } from '~/theme'
import { initializeHighlightJs } from '~/theme'
import '~/theme/highlight.css'
import '@mantine/core/styles.css'
import '@mantine/code-highlight/styles.css'
import '@mantine/notifications/styles.css'
import '~/theme/app.css'

// dayjs is configured in ~/shared/dayjs so that components are self contained.
initializeHighlightJs(hljs) // Initialize highlight.js with languages

const highlightJsAdapter = createHighlightJsAdapter(hljs)

const App = (): React.JSX.Element => (
  <MantineProvider theme={theme} defaultColorScheme="auto">
    <CodeHighlightAdapterProvider adapter={highlightJsAdapter}>
      {/* Replay results are shown inline on the page; toasts are kept for actions whose
          outcome is no longer visible (deleted, rotated token, ...). */}
      <Notifications position="top-right" limit={3} autoClose={4000} />
      <RouterProvider router={createBrowserRouter(routes)} />
    </CodeHighlightAdapterProvider>
  </MantineProvider>
)

createRoot(document.getElementById('root') as HTMLElement).render(<App />)
