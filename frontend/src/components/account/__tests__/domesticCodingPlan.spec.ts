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
  it('kimi uses Coding Plan presets', () => {
    const e = resolveDomesticEndpoints('kimi', '')
    expect(e.chatBaseUrl).toBe('https://api.kimi.com/coding/v1')
    expect(e.anthropicBaseUrl).toBe('https://api.kimi.com/coding')
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
    expect(e.anthropicBaseUrl).toBe('https://api.kimi.com/coding')
  })

  it('minimax uses official paired endpoints', () => {
    const empty = resolveDomesticEndpoints('minimax', '')
    expect(empty.chatBaseUrl).toBe('https://api.minimaxi.com/v1')
    expect(empty.anthropicBaseUrl).toBe('https://api.minimaxi.com/anthropic')
    expect(empty.anthropicSupported).toBe(true)

    const withUrl = resolveDomesticEndpoints('minimax', 'https://api.minimaxi.com/v1')
    expect(withUrl.chatBaseUrl).toBe('https://api.minimaxi.com/v1')
    expect(withUrl.anthropicBaseUrl).toBe('https://api.minimaxi.com/anthropic')
    expect(withUrl.anthropicSupported).toBe(true)

    const explicit = resolveDomesticEndpoints(
      'minimax',
      'https://api.minimaxi.com/v1',
      'https://api.minimaxi.com/anthropic'
    )
    expect(explicit.anthropicBaseUrl).toBe('https://api.minimaxi.com/anthropic')
    expect(explicit.anthropicSupported).toBe(true)
  })

  it('mimo derives the paired anthropic endpoint', () => {
    const withUrl = resolveDomesticEndpoints('mimo', 'https://mimo.example.com/v1')
    expect(withUrl.chatBaseUrl).toBe('https://mimo.example.com/v1')
    expect(withUrl.anthropicBaseUrl).toBe('https://mimo.example.com/anthropic')
    expect(withUrl.anthropicSupported).toBe(true)
  })

  it('volcengine is chat-only (no anthropic variant)', () => {
    const e = resolveDomesticEndpoints('volcengine', '')
    expect(e.chatBaseUrl).toBe('https://ark.cn-beijing.volces.com/api/coding/v3')
    expect(e.anthropicBaseUrl).toBeNull()
    expect(e.anthropicSupported).toBe(false)
    expect(e.noteKey).not.toBe('')
  })

  it('deepseek provides both compatible endpoints', () => {
    const e = resolveDomesticEndpoints('deepseek', '')
    expect(e.chatBaseUrl).toBe('https://api.deepseek.com')
    expect(e.anthropicBaseUrl).toBe('https://api.deepseek.com/anthropic')
    expect(e.anthropicSupported).toBe(true)
  })

  it('custom anthropic compatible is anthropic-only', () => {
    const e = resolveDomesticEndpoints('custom_anthropic_compatible', 'https://anthropic.example.com')
    expect(e.chatBaseUrl).toBe('')
    expect(e.anthropicBaseUrl).toBe('https://anthropic.example.com')
    expect(e.anthropicSupported).toBe(true)
  })
})

describe('buildDomesticAccountPayloads', () => {
  it('kimi: creates one dual-protocol account from one key', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'kimi',
      name: 'My Kimi',
      apiKey: 'sk-kimi',
      inputBaseUrl: ''
    })

    expect(anthropicCreated).toBe(true)
    expect(payloads).toHaveLength(1)

    const account = payloads[0]
    expect(account.name).toBe('My Kimi')
    expect(account.platform).toBe('kimi')
    expect(account.type).toBe('apikey')
    expect(account.credentials.base_url).toBe('https://api.kimi.com/coding/v1')
    expect(account.credentials.anthropic_base_url).toBe('https://api.kimi.com/coding')
    expect(account.credentials.api_key).toBe('sk-kimi')
    expect(account.credentials.wire_api).toBe('dual')
    expect(account.credentials.anthropic_auth_mode).toBe('x-api-key')
    expect(account.extra?.coding_plan_variant).toBe('dual')
    expect(account.extra?.responses_support).toBe('via_external_router')
    expect(account.extra?.messages_support).toBe('anthropic_messages')
  })

  it('stores the API key once on the shared account', () => {
    const { payloads } = buildDomesticAccountPayloads({
      provider: 'zhipu',
      name: 'GLM',
      apiKey: 'shared-key',
      inputBaseUrl: ''
    })
    expect(payloads.map((p) => p.credentials.api_key)).toEqual(['shared-key'])
    expect(payloads[0].credentials.anthropic_auth_mode).toBe('bearer')
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
    expect(payloads[0].name).toBe('Ark')
    expect(payloads[0].extra?.coding_plan_probe_status).toBe('unsupported')
  })

  it('minimax uses presets without requiring duplicate endpoint input', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'minimax',
      name: 'MM',
      apiKey: 'sk-mm',
      inputBaseUrl: ''
    })
    expect(anthropicCreated).toBe(true)
    expect(payloads).toHaveLength(1)
    expect(payloads[0].credentials.base_url).toBe('https://api.minimaxi.com/v1')
    expect(payloads[0].credentials.anthropic_base_url).toBe('https://api.minimaxi.com/anthropic')
  })

  it('minimax keeps explicit endpoint overrides on one account', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'minimax',
      name: 'MM',
      apiKey: 'sk-mm',
      inputBaseUrl: 'https://api.minimaxi.com/v1',
      inputAnthropicBaseUrl: 'https://api.minimaxi.com/anthropic'
    })
    expect(anthropicCreated).toBe(true)
    expect(payloads).toHaveLength(1)
    expect(payloads[0].credentials.anthropic_base_url).toBe('https://api.minimaxi.com/anthropic')
  })

  it('deepseek creates a pure key Chat/Codex account without coding_plan_provider', () => {
    const { payloads, anthropicCreated } = buildDomesticAccountPayloads({
      provider: 'deepseek',
      name: 'DS',
      apiKey: 'sk-ds',
      inputBaseUrl: ''
    })
    expect(anthropicCreated).toBe(true)
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
    expect(payloads[0].name).toBe('CA')
    expect(payloads[0].platform).toBe('custom_anthropic_compatible')
    expect(payloads[0].extra?.coding_plan_provider).toBeUndefined()
  })

  it('binds one account to provider and anthropic groups but never openai', () => {
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
    expect(payloads[0].group_ids).toEqual([1, 2])
  })

  it('passes through model mapping to the shared account', () => {
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
