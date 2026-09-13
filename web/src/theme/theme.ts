import {
  ActionIcon,
  Alert,
  Badge,
  Button,
  Card,
  Modal,
  Pagination,
  Select,
  Switch,
  Table,
  Textarea,
  TextInput,
  Tooltip,
  createTheme,
  rem,
  type MantineColorsTuple,
} from '@mantine/core'

/**
 * Design tokens.
 *
 * The look follows two references:
 *   - TDesign (Tencent): 8px grid, three levels of information hierarchy, dense tables,
 *     enterprise blue as the single accent colour.
 *   - Apple HIG: content over containers (cards use a 1px hairline border, not heavy
 *     shadows), restrained motion (0.2-0.3s ease-out), 44px hit targets.
 *
 * This is a developer tool, so density wins over whitespace: body text is 13/14px,
 * tables are compact, and code uses a monospace stack everywhere.
 */

// Enterprise blue (TDesign #0052d9 as the primary shade), 10 step tuples.
const brand: MantineColorsTuple = [
  '#eef3ff', '#dbe6fe', '#bdd0fd', '#93b4fa', '#6690f6',
  '#3f6df3', '#0052d9', '#0043b3', '#003694', '#002b7a',
]

const success: MantineColorsTuple = [
  '#e3f9f0', '#c1f2e1', '#87e9c3', '#45d9a3', '#16c98a',
  '#00b578', '#00a870', '#008a5c', '#006e49', '#00573a',
]

const warning: MantineColorsTuple = [
  '#fff3e6', '#ffe4cc', '#ffcca3', '#ffb070', '#f79b45',
  '#ed7b2f', '#d96a1f', '#b35c00', '#8f4a00', '#733c00',
]

const error: MantineColorsTuple = [
  '#fdecec', '#fbdada', '#f7b3b0', '#ef8783', '#e5615c',
  '#d54941', '#bd3b34', '#a02f29', '#8a2622', '#731f1b',
]

// Separate hue for "timeout": a warning-amber would be indistinguishable from a
// business failure (4xx/5xx) at a glance.
const amber: MantineColorsTuple = [
  '#fff9e6', '#ffefc2', '#ffe28a', '#ffd34d', '#f5be24',
  '#e0a217', '#c98a00', '#a06e00', '#7a5400', '#5c3f00',
]

// One hairline colour for every container border.
//
// Set explicitly instead of relying on --mantine-color-default-border: Mantine ships its
// own value for that variable, and cards kept Mantine's grey while separators used ours -
// two different greys for "a border" on the same screen.
const hairline = 'light-dark(#e5e7eb, #2b2f36)'

export const MONO =
  "'JetBrains Mono','SF Mono',ui-monospace,'Cascadia Code','Menlo','Consolas','Sarasa Mono SC','Noto Sans Mono CJK SC','Microsoft YaHei',monospace"

export const theme = createTheme({
  primaryColor: 'brand',
  primaryShade: { light: 6, dark: 5 },
  autoContrast: true,
  luminanceThreshold: 0.3,
  colors: { brand, success, warning, error, amber },

  fontFamily:
    "-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Hiragino Sans GB','Microsoft YaHei','Source Han Sans SC','Noto Sans CJK SC',sans-serif",
  fontFamilyMonospace: MONO,

  // Chinese reading sizes: body 13/14, supporting 12, generous line height.
  fontSizes: { xs: rem(12), sm: rem(13), md: rem(14), lg: rem(16), xl: rem(18) },
  lineHeights: { xs: '1.5', sm: '1.6', md: '1.6', lg: '1.5', xl: '1.35' },

  headings: {
    fontWeight: '600',
    sizes: {
      h3: { fontSize: rem(20), lineHeight: '1.4' },
      h4: { fontSize: rem(16), lineHeight: '1.5' },
      h5: { fontSize: rem(14), lineHeight: '1.5' },
      h6: { fontSize: rem(13), lineHeight: '1.5' },
    },
  },

  // Mantine's default xs is 10px, which is not on the 8px grid.
  spacing: { xxs: rem(4), xs: rem(8), sm: rem(12), md: rem(16), lg: rem(24), xl: rem(32) },

  radius: { xs: rem(4), sm: rem(6), md: rem(8), lg: rem(12), xl: rem(16) },
  defaultRadius: 'sm',

  shadows: {
    xs: '0 1px 2px rgba(16,24,40,.05)',
    sm: '0 4px 12px rgba(16,24,40,.08), 0 1px 3px rgba(16,24,40,.04)',
    md: '0 12px 32px rgba(16,24,40,.14), 0 2px 8px rgba(16,24,40,.06)',
  },

  other: { mono: MONO },

  components: {
    Card: Card.extend({
      defaultProps: { radius: 'md', withBorder: true, padding: 'md' },
      styles: { root: { borderColor: hairline } },
    }),
    Table: Table.extend({
      defaultProps: {
        withTableBorder: true,
        highlightOnHover: true,
        verticalSpacing: 'xs',
        horizontalSpacing: 'sm',
        fz: 'xs',
      },
      // A card is a rounded container; a table with a full border was a square one. Two
      // shapes for "a box with things in it" is the kind of detail that makes a UI feel
      // assembled rather than designed, so the table gets the same corner radius.
      styles: {
        table: {
          borderRadius: 'var(--mantine-radius-md)',
          overflow: 'hidden',
          borderColor: hairline,
        },
      },
    }),
    Badge: Badge.extend({
      defaultProps: { variant: 'light', radius: 'sm', fw: 500, size: 'sm' },
      // Mantine's `sm` badge ships 10px text. The theme's own floor for supporting text
      // is 12px (see fontSizes.xs), and 10px Chinese is the one place where the app
      // looked blurry next to everything else - every chip in the app (method, outcome,
      // status) is a Badge, so this is the single place to fix it.
      styles: { root: { fontSize: 'var(--mantine-font-size-xs)' } },
    }),
    // Two sizes, and only two. The rule:
    //   `sm` (36px / 13px) - the page's primary action and nothing else: 新建收件箱,
    //        重放, the empty state buttons, 回到收件箱. One per screen, so the eye has
    //        somewhere to land.
    //   `xs` (30px / 12px) - everything inline: toolbar buttons (复制为 cURL, 删除,
    //        筛选), table row actions, modal footers, command palette rows.
    // Omitting `size` therefore means "this is the CTA"; an inline button has to say
    // `size="xs"` out loud.
    Button: Button.extend({ defaultProps: { size: 'sm', radius: 'sm', fw: 500 } }),
    // 36px visually, 44px hit area (see app.css).
    ActionIcon: ActionIcon.extend({ defaultProps: { variant: 'subtle', radius: 'sm', size: 'md' } }),
    TextInput: TextInput.extend({ defaultProps: { size: 'sm', radius: 'sm' } }),
    Select: Select.extend({ defaultProps: { size: 'sm', radius: 'sm', checkIconPosition: 'right' } }),
    Textarea: Textarea.extend({ defaultProps: { size: 'sm', radius: 'sm' } }),
    Switch: Switch.extend({ defaultProps: { size: 'sm' } }),
    Alert: Alert.extend({ defaultProps: { radius: 'sm' } }),
    Tooltip: Tooltip.extend({ defaultProps: { openDelay: 300, closeDelay: 80, withArrow: true, radius: 'sm', fz: 'xs' } }),
    Modal: Modal.extend({
      defaultProps: { centered: true, radius: 'md', overlayProps: { blur: 2, opacity: 0.45 } },
    }),
    Pagination: Pagination.extend({ defaultProps: { size: 'sm', radius: 'sm', withEdges: true } }),
  },
})
