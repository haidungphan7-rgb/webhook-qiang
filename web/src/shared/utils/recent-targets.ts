import type { AttemptOutcome } from '~/api/v1'

/**
 * The replay targets worth offering again.
 *
 * Debugging a webhook means replaying the same event at the same local service over and
 * over, and typing `http://127.0.0.1:3000/webhook` for the tenth time is the kind of small
 * friction that makes people stop using a tool. These are the addresses this event was
 * already replayed at, newest first, so one click gets the last one back.
 */

export type RecentTarget = {
  url: string
  /** `null` when the address is only known locally (never replayed on this event). */
  outcome: AttemptOutcome | null
}

type AttemptLike = {
  target_url: string
  outcome: AttemptOutcome
  started_at?: string
}

/**
 * Targets to offer, newest first, each one listed once.
 *
 * Two sources, in this order:
 *   1. the event's replay history - it carries the last result, which is the useful part
 *      ("did this address work last time?");
 *   2. the addresses typed in this browser (localStorage), which fall back to history-less
 *      entries so a target survives a cleared server or a different machine.
 *
 * De-duplicated by exact URL, keeping the newest attempt's outcome.
 */
export function recentTargetsFrom(history: AttemptLike[], stored: string[] = [], limit = 5): RecentTarget[] {
  const newestFirst = [...history].sort((a, b) => (b.started_at ?? '').localeCompare(a.started_at ?? ''))

  const seen = new Map<string, RecentTarget>()

  for (const attempt of newestFirst) {
    const url = attempt.target_url?.trim()

    if (!url || seen.has(url)) {
      continue
    }

    seen.set(url, { url, outcome: attempt.outcome })
  }

  for (const url of stored.map((u) => u.trim()).filter(Boolean)) {
    if (!seen.has(url)) {
      seen.set(url, { url, outcome: null })
    }
  }

  return [...seen.values()].slice(0, limit)
}
