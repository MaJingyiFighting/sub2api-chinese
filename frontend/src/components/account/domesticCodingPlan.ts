import type { AccountPlatform, CreateAccountRequest } from '@/types'

/**
 * Domestic Coding Plan provider helpers.
 *
 * A domestic provider is entered once (a single API key + base URL) and is
 * materialized into up to two accounts:
 *   - a Chat/Codex variant (wire_api=openai_chat) that serves /v1/responses via
 *     Chat Completions, and
 *   - an Anthropic variant (wire_api=anthropic_messages) that serves Claude Code
 *     /v1/messages.
 *
 * The user never picks an "API format" and never enters a separate quota URL.
 */

export type DomesticProvider = 'kimi' | 'zhipu' | 'minimax' | 'volcengine' | 'mimo'

export const DOMESTIC_PROVIDERS: DomesticProvider[] = ['kimi', 'zhipu', 'minimax', 'volcengine', 'mimo']

export interface DomesticProviderTab {
  id: DomesticProvider
  label: string
  activeColor: string
}

export const DOMESTIC_PROVIDER_TABS: DomesticProviderTab[] = [
  { id: 'kimi', label: 'Kimi', activeColor: 'text-gray-800 dark:text-gray-200' },
  { id: 'zhipu', label: 'Zhipu', activeColor: 'text-indigo-600 dark:text-indigo-400' },
  { id: 'minimax', label: 'MiniMax', activeColor: 'text-rose-600 dark:text-rose-400' },
  { id: 'volcengine', label: 'Volcengine', activeColor: 'text-blue-500 dark:text-blue-300' },
  { id: 'mimo', label: 'MiMo', activeColor: 'text-teal-600 dark:text-teal-400' }
]

export function isDomesticCodingPlanPlatform(platform: string): platform is DomesticProvider {
  return (DOMESTIC_PROVIDERS as string[]).includes(platform)
}

export interface DomesticEndpoints {
  /** Base URL for the Chat/Codex (Chat Completions) variant. */
  chatBaseUrl: string
  /** Base URL for the Anthropic Messages variant, or null when unsupported. */
  anthropicBaseUrl: string | null
  /** Whether an Anthropic Messages variant should be created. */
  anthropicSupported: boolean
  /**
   * i18n key for a UI note explaining a non-obvious endpoint decision
   * (ambiguous Anthropic endpoint, or chat-only provider). Empty when none.
   */
  noteKey: string
}

function trimTrailingSlash(url: string): string {
  return url.trim().replace(/\/+$/, '')
}

/**
 * resolveDomesticEndpoints maps a provider + the single base URL the user typed
 * to the concrete Chat and Anthropic endpoints, applying provider presets.
 *
 * Presets:
 *   - kimi:  chat https://api.moonshot.cn/v1            anthropic https://api.moonshot.cn/anthropic
 *   - zhipu: chat https://open.bigmodel.cn/api/coding/paas/v4  anthropic https://open.bigmodel.cn/api/anthropic
 *   - minimax/mimo: base URL is required; the Anthropic endpoint can't be
 *     reliably derived, so it reuses the same base and surfaces a note to edit
 *     the advanced address later if it differs.
 *   - volcengine: chat https://ark.cn-beijing.volces.com/api/v3; Ark exposes no
 *     Anthropic Messages endpoint, so only the Chat/Codex account is created.
 */
export function resolveDomesticEndpoints(provider: string, inputBaseUrl: string): DomesticEndpoints {
  const input = trimTrailingSlash(inputBaseUrl || '')
  switch (provider) {
    case 'kimi':
      return {
        chatBaseUrl: input || 'https://api.moonshot.cn/v1',
        anthropicBaseUrl: 'https://api.moonshot.cn/anthropic',
        anthropicSupported: true,
        noteKey: ''
      }
    case 'zhipu':
      return {
        chatBaseUrl: input || 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropicBaseUrl: 'https://open.bigmodel.cn/api/anthropic',
        anthropicSupported: true,
        noteKey: ''
      }
    case 'minimax':
      return {
        chatBaseUrl: input,
        anthropicBaseUrl: input || null,
        anthropicSupported: input !== '',
        noteKey: 'admin.accounts.codingPlan.anthropicSameBaseNote'
      }
    case 'mimo':
      return {
        chatBaseUrl: input,
        anthropicBaseUrl: input || null,
        anthropicSupported: input !== '',
        noteKey: 'admin.accounts.codingPlan.anthropicSameBaseNote'
      }
    case 'volcengine':
      return {
        chatBaseUrl: input || 'https://ark.cn-beijing.volces.com/api/v3',
        anthropicBaseUrl: null,
        anthropicSupported: false,
        noteKey: 'admin.accounts.codingPlan.chatOnlyNote'
      }
    default:
      return {
        chatBaseUrl: input,
        anthropicBaseUrl: null,
        anthropicSupported: false,
        noteKey: ''
      }
  }
}

export interface DomesticGroupInfo {
  id: number
  platform: string
}

export interface BuildDomesticPayloadsOptions {
  provider: DomesticProvider
  name: string
  notes?: string | null
  apiKey: string
  inputBaseUrl: string
  modelMapping?: Record<string, string> | null
  /** All selectable groups (for splitting by platform). */
  groups?: DomesticGroupInfo[]
  /** Group IDs the user selected. */
  selectedGroupIds?: number[]
  baseExtra?: Record<string, unknown>
  baseCredentials?: Record<string, unknown>
  proxyId?: number | null
  concurrency?: number
  loadFactor?: number | null
  priority?: number
  rateMultiplier?: number
  expiresAt?: number | null
  autoPauseOnExpired?: boolean
}

export interface BuildDomesticPayloadsResult {
  payloads: CreateAccountRequest[]
  anthropicCreated: boolean
  endpoints: DomesticEndpoints
}

function probeStatusFor(provider: DomesticProvider): 'supported' | 'experimental' {
  return provider === 'volcengine' || provider === 'mimo' ? 'experimental' : 'supported'
}

/**
 * Split the user's selected groups for each variant.
 *   - Chat/Codex variant may only join groups of its own provider platform
 *     (domestic provider isolation; never OpenAI/ChatGPT).
 *   - Anthropic variant may additionally join Anthropic-platform groups (Claude
 *     Code), in line with the backend's group-binding rules.
 */
function splitGroupIds(
  provider: DomesticProvider,
  groups: DomesticGroupInfo[],
  selectedGroupIds: number[]
): { chatGroupIds: number[]; anthropicGroupIds: number[] } {
  if (selectedGroupIds.length === 0) {
    return { chatGroupIds: [], anthropicGroupIds: [] }
  }
  const platformById = new Map(groups.map((g) => [g.id, g.platform]))
  const chatGroupIds: number[] = []
  const anthropicGroupIds: number[] = []
  for (const id of selectedGroupIds) {
    const platform = platformById.get(id)
    // Unknown platform (group not in the provided list): be permissive and let
    // the backend validate — include it for both variants.
    if (platform === undefined || platform === provider) {
      chatGroupIds.push(id)
      anthropicGroupIds.push(id)
    } else if (platform === 'anthropic') {
      anthropicGroupIds.push(id)
    }
    // Any other platform (e.g. openai) is dropped: a domestic account can never
    // join it.
  }
  return { chatGroupIds, anthropicGroupIds }
}

/**
 * buildDomesticAccountPayloads turns a single domestic-provider form submission
 * into the one or two CreateAccountRequest payloads to POST. The same API key is
 * reused for both variants — the user only enters it once.
 */
export function buildDomesticAccountPayloads(
  opts: BuildDomesticPayloadsOptions
): BuildDomesticPayloadsResult {
  const endpoints = resolveDomesticEndpoints(opts.provider, opts.inputBaseUrl)
  const probeStatus = probeStatusFor(opts.provider)
  const { chatGroupIds, anthropicGroupIds } = splitGroupIds(
    opts.provider,
    opts.groups ?? [],
    opts.selectedGroupIds ?? []
  )

  const sharedFields = {
    proxy_id: opts.proxyId ?? null,
    concurrency: opts.concurrency,
    load_factor: opts.loadFactor ?? undefined,
    priority: opts.priority,
    rate_multiplier: opts.rateMultiplier,
    expires_at: opts.expiresAt ?? null,
    auto_pause_on_expired: opts.autoPauseOnExpired
  }

  const modelMapping =
    opts.modelMapping && Object.keys(opts.modelMapping).length > 0 ? opts.modelMapping : undefined

  const chatCredentials: Record<string, unknown> = {
    ...(opts.baseCredentials ?? {}),
    base_url: endpoints.chatBaseUrl,
    api_key: opts.apiKey,
    wire_api: 'openai_chat',
    api_format: 'chat_completions',
    chat_completions_route_enabled: true,
    responses_route_enabled: true
  }
  if (modelMapping) {
    chatCredentials.model_mapping = modelMapping
  }

  const chatPayload: CreateAccountRequest = {
    name: `${opts.name} - Chat/Codex`,
    notes: opts.notes ?? null,
    platform: opts.provider as AccountPlatform,
    type: 'apikey',
    credentials: chatCredentials,
    extra: {
      ...(opts.baseExtra ?? {}),
      coding_plan_provider: opts.provider,
      coding_plan_variant: 'chat',
      responses_support: 'via_chat_completions',
      coding_plan_probe_status: probeStatus
    },
    group_ids: chatGroupIds,
    ...sharedFields
  }

  const payloads: CreateAccountRequest[] = [chatPayload]
  let anthropicCreated = false

  if (endpoints.anthropicSupported && endpoints.anthropicBaseUrl) {
    anthropicCreated = true
    const anthropicCredentials: Record<string, unknown> = {
      base_url: endpoints.anthropicBaseUrl,
      api_key: opts.apiKey,
      wire_api: 'anthropic_messages',
      api_format: 'anthropic_messages'
    }
    if (modelMapping) {
      anthropicCredentials.model_mapping = modelMapping
    }
    payloads.push({
      name: `${opts.name} - Claude Code`,
      notes: opts.notes ?? null,
      platform: opts.provider as AccountPlatform,
      type: 'apikey',
      credentials: anthropicCredentials,
      extra: {
        coding_plan_provider: opts.provider,
        coding_plan_variant: 'anthropic',
        responses_support: 'via_anthropic_messages',
        messages_support: 'anthropic_messages',
        coding_plan_probe_status: probeStatus
      },
      group_ids: anthropicGroupIds,
      ...sharedFields
    })
  }

  return { payloads, anthropicCreated, endpoints }
}
