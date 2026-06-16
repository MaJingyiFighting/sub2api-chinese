import { describe, it, expect } from 'vitest'
import {
  resolveDomesticEndpoints,
  buildDomesticAccountPayloads,
  isDomesticCodingPlanPlatform,
  DOMESTIC_PROVIDERS
} from '../domesticCodingPlan'

describe('isDomesticCodingPlanPlatform', () => {
  it('recognizes the five domestic providers', () => {
    for (const p of [
      'kimi',
      'zhipu',
      'minimax',
      'volcengine',
      'mimo',
      'deepseek',
      'custom_openai_compatible',
      'custom_anthropic_compatible'
    ]) {
      expect(isDomesticCodingPlanPlatform(p)).toBe(true)
    }
  })
  it('rejects non-domestic platforms', () => {
    for (const p of ['anthropic', 'openai', 'gemini', 'antigravity', 'domestic', '']) {
      expect(isDomesticCodingPlanPlatform(p)).toBe(false)
    }
  })
  it('exposes coding plan and pure key providers', () => {
    expect(DOMESTIC_PROVIDERS).toHaveLength(8)
  })
})

describe('resolveDomesticEndpoints', () => {
  it('kimi uses moonshot presets', () => {
    const e = resolveDomesticEndpoints('kimi', '')
    expect(e.chatBaseUrl).toBe('https://api.moonshot.cn/v1')
    expect(e.anthropicBaseUrl).toBe('https://api.moonshot.cn/anthropic')
    expect(e.anthropicSupported).toBe(true)
  })

  it('zhipu uses bigmodel presets', () => {
    const e = resolveDomesticEndpoints('zhipu', '')
    expect(e.chatBaseUrl).toBe('https://open.bigmodel.cn/api/coding/paas/v4')
    expect(e.anthropicBaseUrl).toBe('https://open.bigmodel.cn/api/anthropic')
    expect(e.anthropicSupported).toBe(true)
  })

  it('kimi honors a custom chat base url but keeps the anthropic preset', () => {
    const e = resolveDomesticEndpoints('kimi', 'https://proxy.example.com/v1/')
    expect(e.chatBaseUrl).toBe('https://proxy.example.com/v1')
    expect(e.anthropicBaseUrl).toBe('https://api.moonshot.cn/anthropic')
  })

  it('minimax requires explicit anthropic base url before creating anthropic variant', () => {
    const empty = resolveDomesticEndpoints('minimax', '')
    expect(empty.chatBaseUrl).toBe('')
    expect(empty.anthropicSupported).toBe(false)

    const withUrl = resolveDomesticEndpoints('minimax', 'https://api.minimaxi.com/v1')
    expect(withUrl.chatBaseUrl).toBe('https://api.minimaxi.com/v1')
    expect(withUrl.anthropicBaseUrl).toBeNull()
    expect(withUrl.anthropicSupported).toBe(false)
    expect(withUrl.noteKey).not.toBe('')

    const explicit = resolveDomesticEndpoints(
      'minimax',
      'https://api.minimaxi.com/v1',
      'https://api.minimaxi.com/anthropic'
    )
    expect(explicit.anthropicBaseUrl).toBe('https://api.minimaxi.com/anthropic')
    expect(explicit.anthropicSupported).toBe(true)
  })

  it('mimo requires explicit anthropic base url', () => {
    const withUrl = resolveDomesticEndpoints('mimo', 'https://mimo.example.com/v1')
    expect(withUrl.chatBaseUrl).toBe('https://mimo.example.com/v1')
    expect(withUrl.anthropicSupported).toBe(false)
    expect(withUrl.noteKey).not.toBe('')
  })

  it('volcengine is chat-only (no anthropic variant)', () => {
    const e = resolveDomesticEndpoints('volcengine', '')
    expect(e.chatBaseUrl).toBe('https://ark.cn-beijing.volces.com/api/v3')
    expect(e.anthropicBaseUrl).toBeNull()
    expect(e.anthropicSupported).toBe(false)
    expect(e.noteKey).not.toBe('')
  })

  it('deepseek is chat-only with a default base url', () => {
    const e = resolveDomesticEndpoints('deepseek', '')
    expect(e.chatBaseUrl).toBe('https://api.deepseek.com')
    expect(e.anthropicSupported).toBe(false)
  })

  it('custom anthropic compatible is anthropic-only', () => {
    const e = resolveDomesticEndpoints('custom_anthropic_compatible', 'https://anthropic.example.com')
    expect(e.chatBaseUrl).toBe('')
    expect(e.anthropicBaseUrl).toBe('https://anthropic.example.com')
    expect(e.anthropicSupported).toBe(true)
  })
})

describe('buildDomesticAccountPayloads', () => {
  it('kimi: creates a Chat/Codex and a Claude Code account from one key', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'kimi',
      name: 'My Kimi',
      apiKey: 'sk-kimi',
      inputBaseUrl: ''
    })

    expect(anthropicCreated).toBe(true)
    expect(payloads).toHaveLength(2)

    const chat = payloads[0]
    expect(chat.name).toBe('My Kimi - Chat/Codex')
    expect(chat.platform).toBe('kimi')
    expect(chat.type).toBe('apikey')
    expect(chat.credentials.base_url).toBe('https://api.moonshot.cn/v1')
    expect(chat.credentials.api_key).toBe('sk-kimi')
    expect(chat.credentials.wire_api).toBe('openai_chat')
    expect(chat.credentials.responses_route_enabled).toBe(true)
    expect(chat.extra?.coding_plan_variant).toBe('chat')
    expect(chat.extra?.responses_support).toBe('via_chat_completions')

    const anth = payloads[1]
    expect(anth.name).toBe('My Kimi - Claude Code')
    expect(anth.platform).toBe('kimi')
    expect(anth.credentials.base_url).toBe('https://api.moonshot.cn/anthropic')
    expect(anth.credentials.api_key).toBe('sk-kimi')
    expect(anth.credentials.wire_api).toBe('anthropic_messages')
    expect(anth.extra?.coding_plan_variant).toBe('anthropic')
    expect(anth.extra?.messages_support).toBe('anthropic_messages')
  })

  it('reuses the same API key for both variants', () => {
    const { payloads } = buildDomesticAccountPayloads({
      provider: 'zhipu',
      name: 'GLM',
      apiKey: 'shared-key',
      inputBaseUrl: ''
    })
    expect(payloads.map((p) => p.credentials.api_key)).toEqual(['shared-key', 'shared-key'])
  })

  it('volcengine: creates only the Chat/Codex account', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'volcengine',
      name: 'Ark',
      apiKey: 'sk-volc',
      inputBaseUrl: ''
    })
    expect(anthropicCreated).toBe(false)
    expect(payloads).toHaveLength(1)
    expect(payloads[0].name).toBe('Ark - Chat/Codex')
    expect(payloads[0].extra?.coding_plan_probe_status).toBe('experimental')
  })

  it('minimax without a base url creates no payloads because the UI must require it', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'minimax',
      name: 'MM',
      apiKey: 'sk-mm',
      inputBaseUrl: ''
    })
    expect(anthropicCreated).toBe(false)
    expect(payloads).toHaveLength(0)
  })

  it('minimax with an explicit anthropic base url creates both variants', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'minimax',
      name: 'MM',
      apiKey: 'sk-mm',
      inputBaseUrl: 'https://api.minimaxi.com/v1',
      inputAnthropicBaseUrl: 'https://api.minimaxi.com/anthropic'
    })
    expect(anthropicCreated).toBe(true)
    expect(payloads).toHaveLength(2)
    expect(payloads[1].credentials.base_url).toBe('https://api.minimaxi.com/anthropic')
  })

  it('deepseek creates a pure key Chat/Codex account without coding_plan_provider', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'deepseek',
      name: 'DS',
      apiKey: 'sk-ds',
      inputBaseUrl: ''
    })
    expect(anthropicCreated).toBe(false)
    expect(payloads).toHaveLength(1)
    expect(payloads[0].platform).toBe('deepseek')
    expect(payloads[0].credentials.base_url).toBe('https://api.deepseek.com')
    expect(payloads[0].extra?.coding_plan_provider).toBeUndefined()
  })

  it('custom anthropic compatible creates only the Claude Code account', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'custom_anthropic_compatible',
      name: 'CA',
      apiKey: 'sk-ca',
      inputBaseUrl: 'https://anthropic.example.com'
    })
    expect(anthropicCreated).toBe(true)
    expect(payloads).toHaveLength(1)
    expect(payloads[0].name).toBe('CA - Claude Code')
    expect(payloads[0].platform).toBe('custom_anthropic_compatible')
    expect(payloads[0].extra?.coding_plan_provider).toBeUndefined()
  })

  it('splits selected groups: chat keeps provider groups, anthropic also gets anthropic groups', () => {
    const { payloads } = buildDomesticAccountPayloads({
      provider: 'kimi',
      name: 'K',
      apiKey: 'sk',
      inputBaseUrl: '',
      groups: [
        { id: 1, platform: 'kimi' },
        { id: 2, platform: 'anthropic' },
        { id: 3, platform: 'openai' }
      ],
      selectedGroupIds: [1, 2, 3]
    })
    const chat = payloads[0]
    const anth = payloads[1]
    // Chat variant: only the kimi group (never anthropic, never openai).
    expect(chat.group_ids).toEqual([1])
    // Anthropic variant: kimi + anthropic groups, never openai.
    expect(anth.group_ids).toEqual([1, 2])
  })

  it('passes through model mapping to both variants', () => {
    const { payloads } = buildDomesticAccountPayloads({
      provider: 'kimi',
      name: 'K',
      apiKey: 'sk',
      inputBaseUrl: '',
      modelMapping: { 'claude-3-5-sonnet': 'kimi-k2-turbo-preview' }
    })
    expect(payloads[0].credentials.model_mapping).toEqual({
      'claude-3-5-sonnet': 'kimi-k2-turbo-preview'
    })
    expect(payloads[1].credentials.model_mapping).toEqual({
      'claude-3-5-sonnet': 'kimi-k2-turbo-preview'
    })
  })

  it('does not leak an empty model_mapping', () => {
    const { payloads } = buildDomesticAccountPayloads({
      provider: 'kimi',
      name: 'K',
      apiKey: 'sk',
      inputBaseUrl: '',
      modelMapping: {}
    })
    expect('model_mapping' in payloads[0].credentials).toBe(false)
  })
})
