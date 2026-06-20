import type { AccountPlatform, CreateAccountRequest } from '@/types'

/**
 * Domestic Coding Plan provider helpers.
 *
 * A domestic provider is entered once and stored as one physical account with
 * an OpenAI-compatible Chat endpoint plus an optional Anthropic-compatible
 * Messages endpoint. Codex Responses conversion lives in the optional sidecar.
 *
 * The user never picks an "API format" and never enters a separate quota URL.
 */

export type DomesticProvider =
  | 'kimi'
  | 'zhipu'
  | 'minimax'
  | 'volcengine'
  | 'mimo'
  | 'deepseek'
  | 'custom_openai_compatible'
  | 'custom_anthropic_compatible'

export const DOMESTIC_PROVIDERS: DomesticProvider[] = [
  'kimi',
  'zhipu',
  'minimax',
  'volcengine',
  'mimo',
  'deepseek',
  'custom_openai_compatible',
  'custom_anthropic_compatible'
]

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
  { id: 'mimo', label: 'MiMo', activeColor: 'text-teal-600 dark:text-teal-400' },
  { id: 'deepseek', label: 'DeepSeek', activeColor: 'text-sky-600 dark:text-sky-400' },
  { id: 'custom_openai_compatible', label: 'OpenAI-compatible', activeColor: 'text-emerald-600 dark:text-emerald-400' },
  { id: 'custom_anthropic_compatible', label: 'Anthropic-compatible', activeColor: 'text-orange-600 dark:text-orange-400' }
]

export function isDomesticCodingPlanPlatform(platform: string): platform is DomesticProvider {
  return (DOMESTIC_PROVIDERS as string[]).includes(platform)
}

export interface DomesticEndpoints {
  /** Base URL for the Chat/Codex (Chat Completions) variant. */
  chatBaseUrl: string
  /** Base URL for the Anthropic Messages variant, or null when unsupported. */
  anthropicBaseUrl: string | null
  /** Whether the shared account supports the Anthropic Messages endpoint. */
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
 *   - kimi:  chat https://api.kimi.com/coding/v1        anthropic https://api.kimi.com/coding
 *   - zhipu: chat https://open.bigmodel.cn/api/coding/paas/v4  anthropic https://open.bigmodel.cn/api/anthropic
 *   - minimax: official token-plan OpenAI and Anthropic compatible endpoints.
 *   - mimo: defaults to the China token-plan region and derives its paired endpoint.
 *   - volcengine: chat https://ark.cn-beijing.volces.com/api/coding/v3; Ark exposes no
 *     Anthropic Messages endpoint, so only the Chat/Codex account is created.
 */
export function resolveDomesticEndpoints(
  provider: string,
  inputBaseUrl: string,
  inputAnthropicBaseUrl = ''
): DomesticEndpoints {
  const input = trimTrailingSlash(inputBaseUrl || '')
  const anthropicInput = trimTrailingSlash(inputAnthropicBaseUrl || '')
  switch (provider) {
    case 'kimi':
      return {
        chatBaseUrl: input || 'https://api.kimi.com/coding/v1',
        anthropicBaseUrl: anthropicInput || 'https://api.kimi.com/coding',
        anthropicSupported: true,
        noteKey: ''
      }
    case 'zhipu':
      return {
        chatBaseUrl: input || 'https://open.bigmodel.cn/api/coding/paas/v4',
        anthropicBaseUrl: anthropicInput || 'https://open.bigmodel.cn/api/anthropic',
        anthropicSupported: true,
        noteKey: ''
      }
    case 'minimax':
      return {
        chatBaseUrl: input || 'https://api.minimaxi.com/v1',
        anthropicBaseUrl: anthropicInput || 'https://api.minimaxi.com/anthropic',
        anthropicSupported: true,
        noteKey: ''
      }
    case 'mimo':
      {
        const chatBaseUrl = input || 'https://token-plan-cn.xiaomimimo.com/v1'
        const root = chatBaseUrl.replace(/\/v1$/, '')
        return {
          chatBaseUrl,
          anthropicBaseUrl: anthropicInput || `${root}/anthropic`,
          anthropicSupported: true,
          noteKey: ''
        }
      }
    case 'volcengine':
      return {
        chatBaseUrl: input || 'https://ark.cn-beijing.volces.com/api/coding/v3',
        anthropicBaseUrl: null,
        anthropicSupported: false,
        noteKey: 'admin.accounts.codingPlan.chatOnlyNote'
      }
    case 'deepseek':
      return {
        chatBaseUrl: input || 'https://api.deepseek.com',
        anthropicBaseUrl: anthropicInput || 'https://api.deepseek.com/anthropic',
        anthropicSupported: true,
        noteKey: ''
      }
    case 'custom_openai_compatible':
      return {
        chatBaseUrl: input,
        anthropicBaseUrl: null,
        anthropicSupported: false,
        noteKey: 'admin.accounts.codingPlan.chatOnlyNote'
      }
    case 'custom_anthropic_compatible':
      return {
        chatBaseUrl: '',
        anthropicBaseUrl: input || null,
        anthropicSupported: input !== '',
        noteKey: 'admin.accounts.codingPlan.anthropicOnlyNote'
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
  inputAnthropicBaseUrl?: string
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

function probeStatusFor(provider: DomesticProvider): 'supported' | 'experimental' | 'unsupported' {
  if (provider === 'volcengine' || provider === 'mimo' || provider === 'deepseek' || provider === 'custom_openai_compatible' || provider === 'custom_anthropic_compatible') {
    return 'unsupported'
  }
  return 'supported'
}

/**
 * Split the user's selected groups for each variant.
 * A domestic account is one physical account with two optional protocol
 * endpoints, so all compatible group bindings stay on the same health/quota
 * record.
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
    } else if (platform === 'domestic') {
      chatGroupIds.push(id)
      anthropicGroupIds.push(id)
    }
    // Any other platform (e.g. openai) is dropped: a domestic account can never
    // join it.
  }
  return { chatGroupIds, anthropicGroupIds }
}

/**
 * buildDomesticAccountPayloads turns a single domestic-provider form submission
 * into one physical account carrying both protocol endpoints.
 */
export function buildDomesticAccountPayloads(
  opts: BuildDomesticPayloadsOptions
): BuildDomesticPayloadsResult {
  const endpoints = resolveDomesticEndpoints(opts.provider, opts.inputBaseUrl, opts.inputAnthropicBaseUrl)
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

  const credentials: Record<string, unknown> = {
    ...(opts.baseCredentials ?? {}),
    api_key: opts.apiKey,
    wire_api: endpoints.chatBaseUrl && endpoints.anthropicBaseUrl ? 'dual' : endpoints.chatBaseUrl ? 'openai_chat' : 'anthropic_messages'
  }
  if (endpoints.chatBaseUrl) {
    credentials.base_url = endpoints.chatBaseUrl
    credentials.api_format = 'chat_completions'
    credentials.chat_completions_route_enabled = true
    credentials.openai_capabilities = ['chat_completions']
  }
  if (endpoints.anthropicBaseUrl) {
    credentials.anthropic_base_url = endpoints.anthropicBaseUrl
    credentials.anthropic_auth_mode =
      opts.provider === 'zhipu' || opts.provider === 'minimax' || opts.provider === 'mimo'
        ? 'bearer'
        : 'x-api-key'
  }
  if (modelMapping) {
    credentials.model_mapping = modelMapping
  }

  const groupIds = Array.from(new Set([...chatGroupIds, ...anthropicGroupIds]))
  const payloads: CreateAccountRequest[] = [{
    name: opts.name,
    notes: opts.notes ?? null,
    platform: opts.provider as AccountPlatform,
    type: 'apikey',
    credentials,
    extra: {
      ...(opts.baseExtra ?? {}),
      ...(isDomesticCodingPlanPlatform(opts.provider) && !opts.provider.startsWith('custom_') && opts.provider !== 'deepseek'
        ? { coding_plan_provider: opts.provider }
        : {}),
      coding_plan_variant: endpoints.chatBaseUrl && endpoints.anthropicBaseUrl ? 'dual' : endpoints.chatBaseUrl ? 'chat' : 'anthropic',
      openai_responses_supported: false,
      responses_support: endpoints.chatBaseUrl ? 'via_external_router' : 'unsupported',
      messages_support: endpoints.anthropicBaseUrl ? 'anthropic_messages' : 'unsupported',
      anthropic_passthrough: Boolean(endpoints.anthropicBaseUrl),
      coding_plan_probe_status: probeStatus
    },
    group_ids: groupIds,
    ...sharedFields
  }]

  return { payloads, anthropicCreated: Boolean(endpoints.anthropicBaseUrl), endpoints }
}
