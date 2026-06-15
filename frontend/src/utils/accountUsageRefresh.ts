import type { Account } from '@/types'

const normalizeUsageRefreshValue = (value: unknown): string => {
  if (value == null) return ''
  return String(value)
}

export const buildOpenAIUsageRefreshKey = (account: Pick<Account, 'id' | 'platform' | 'type' | 'updated_at' | 'last_used_at' | 'rate_limit_reset_at' | 'extra'>): string => {
  if (account.platform !== 'openai') {
    return ''
  }

  const extra = account.extra ?? {}
  const isCodingPlanAccount =
    account.type === 'apikey' &&
    typeof extra.coding_plan_provider === 'string' &&
    extra.coding_plan_provider.trim() !== ''

  if (account.type !== 'oauth' && !isCodingPlanAccount) {
    return ''
  }

  return [
    account.id,
    account.updated_at,
    account.last_used_at,
    account.rate_limit_reset_at,
    extra.codex_usage_updated_at,
    extra.codex_5h_used_percent,
    extra.codex_5h_reset_at,
    extra.codex_5h_reset_after_seconds,
    extra.codex_5h_window_minutes,
    extra.codex_7d_used_percent,
    extra.codex_7d_reset_at,
    extra.codex_7d_reset_after_seconds,
    extra.codex_7d_window_minutes,
    extra.coding_plan_provider,
    extra.coding_plan_probe_status,
    extra.coding_plan_usage_updated_at,
    extra.coding_plan_5h_used_percent,
    extra.coding_plan_5h_reset_at,
    extra.coding_plan_5h_reset_after_seconds,
    extra.coding_plan_weekly_used_percent,
    extra.coding_plan_weekly_reset_at,
    extra.coding_plan_weekly_reset_after_seconds,
    extra.coding_plan_account_status
  ].map(normalizeUsageRefreshValue).join('|')
}
