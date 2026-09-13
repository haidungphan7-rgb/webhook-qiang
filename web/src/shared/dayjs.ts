import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import 'dayjs/locale/zh-cn'

// Configured once, here, instead of in main.tsx.
//
// If the plugin and the locale are only registered by the entry point, every component
// that renders a relative time works in the app but crashes as soon as it is rendered on
// its own - a component test being the obvious example. Importing dayjs from this module
// makes the configuration part of the dependency, not of the startup sequence.
dayjs.extend(relativeTime) // https://day.js.org/docs/en/plugin/relative-time
dayjs.locale('zh-cn') // relative times ("3 分钟前") must match the rest of the UI

export { dayjs }
