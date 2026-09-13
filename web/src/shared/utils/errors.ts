import { ApiError } from '~/api/v1'

// The API answers with machine readable codes and English sentences. Showing those
// sentences in a Chinese UI looks unfinished, so every error goes through here first.
const BY_CODE: Record<string, string> = {
  rate_limited: '重放太频繁了，请稍后再试',
  target_blocked: '目标地址被安全策略拒绝（内网 / 保留地址 / 非 http(s)）',
  invalid_target: '目标地址不合法，请填写完整的 http(s) 地址',
  validation_error: '填写的内容不符合要求',
  invalid_id: '链接无效，请从列表重新进入',
  invalid_json: '请求格式有误',
  unauthorized: '没有权限，请检查访问密钥',
  not_found: '找不到这个资源，它可能已被删除',
  inbox_disabled: '该收件箱已停用，先在列表里启用它',
  body_too_large: '请求体超过大小上限，已拒绝接收',
  encryption_required: '需要先配置加密密钥才能保存签名密钥',
}

const BY_STATUS: Record<number, string> = {
  400: '请求有误，请检查后重试',
  401: '没有权限，请检查访问密钥',
  403: '没有权限执行这个操作',
  404: '找不到这个资源，它可能已被删除',
  409: '与现有数据冲突，请刷新后重试',
  413: '请求体超过大小上限',
  422: '填写的内容不符合要求',
  429: '操作太频繁了，请稍后再试',
  500: '服务出错了，请查看服务日志',
  503: '服务暂时不可用，请稍后重试',
}

/**
 * Turns any thrown value into a sentence that can be shown to a user.
 *
 * Server messages are deliberately not passed through: they are written for logs, and
 * leaking them turns a Chinese interface into a mixed-language one.
 */
export function humanize(err: unknown): string {
  if (err instanceof ApiError) {
    return BY_CODE[err.code] ?? BY_STATUS[err.status] ?? '操作失败，请重试'
  }

  if (err instanceof Error) {
    // Trimmed: an all-whitespace message would render as an empty alert box.
    const text = err.message.trim()

    if (text !== '' && !/fetch|network|failed to fetch/i.test(text)) {
      return text
    }
  }

  return '操作失败，请重试'
}
