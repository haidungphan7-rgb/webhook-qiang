import type { RouteObject } from 'react-router-dom'
import Layout from '~/screens/layout'
import { NotFoundScreen } from '~/screens/not-found'
import { InboxesScreen } from '~/screens/inboxes'
import { InboxScreen } from '~/screens/inbox'
import { EventScreen } from '~/screens/event'
import { HelpScreen } from '~/screens/help/screen'

/**
 * Four screens, matching the task's minimum page scope:
 *   /                              inbox list + creation
 *   /inboxes/:id                   event list (filters, paging, realtime)
 *   /inboxes/:id/events/:eid       event detail + replay + replay history
 */
export const routes: RouteObject[] = [
  {
    path: '/',
    element: <Layout />,
    children: [
      { index: true, element: <InboxesScreen /> },
      { path: 'inboxes/:id', element: <InboxScreen /> },
      { path: 'inboxes/:id/events/:eid', element: <EventScreen /> },
      // A real page, not a modal: people follow the guide while sending requests, and a
      // modal would cover the very thing they are working with.
      { path: 'help', element: <HelpScreen /> },
      { path: '*', element: <NotFoundScreen /> },
    ],
  },
]
