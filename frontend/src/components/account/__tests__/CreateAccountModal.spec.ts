import { describe, expect, it, vi, beforeEach } from 'vitest'
import { defineComponent, ref } from 'vue'
import { mount } from '@vue/test-utils'

const { createAccountMock } = vi.hoisted(() => ({
  createAccountMock: vi.fn()
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isSimpleMode: true })
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      create: createAccountMock,
      checkMixedChannelRisk: vi.fn().mockResolvedValue({ has_risk: false })
    },
    settings: {
      getWebSearchEmulationConfig: vi.fn().mockResolvedValue({ enabled: false, providers: [] }),
      getSettings: vi.fn().mockResolvedValue({})
    },
    tlsFingerprintProfiles: {
      list: vi.fn().mockResolvedValue([])
    }
  }
}))

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn().mockResolvedValue([])
}))

function fakeOAuth() {
  return {
    authUrl: ref(''),
    sessionId: ref(''),
    loading: ref(false),
    error: ref(''),
    oauthState: ref(''),
    resetState: vi.fn(),
    generateAuthUrl: vi.fn(),
    exchangeAuthCode: vi.fn(),
    buildCredentials: vi.fn(() => ({})),
    buildExtraInfo: vi.fn(() => ({}))
  }
}

vi.mock('@/composables/useOpenAIOAuth', () => ({ useOpenAIOAuth: () => fakeOAuth() }))
vi.mock('@/composables/useGeminiOAuth', () => ({ useGeminiOAuth: () => fakeOAuth() }))
vi.mock('@/composables/useAntigravityOAuth', () => ({ useAntigravityOAuth: () => fakeOAuth() }))
vi.mock('@/composables/useOAuth', () => ({ useOAuth: () => fakeOAuth() }))

vi.mock('@/composables/useQuotaNotifyState', () => ({
  useQuotaNotifyState: () => ({
    globalEnabled: ref(false),
    state: ref({
      daily: { enabled: false, threshold: 0, thresholdType: 'percent' },
      weekly: { enabled: false, threshold: 0, thresholdType: 'percent' },
      total: { enabled: false, threshold: 0, thresholdType: 'percent' }
    }),
    loadGlobalState: vi.fn(),
    loadFromExtra: vi.fn(),
    writeToExtra: vi.fn(),
    reset: vi.fn()
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({ t: (key: string) => key })
  }
})

import CreateAccountModal from '../CreateAccountModal.vue'

const BaseDialogStub = defineComponent({
  name: 'BaseDialog',
  props: { show: { type: Boolean, default: false } },
  template: '<div v-if="show"><slot /><slot name="footer" /></div>'
})

function mountModal() {
  return mount(CreateAccountModal, {
    props: { show: true, proxies: [], groups: [] },
    global: {
      stubs: {
        BaseDialog: BaseDialogStub,
        Icon: true,
        PlatformIcon: true,
        ProxySelector: true,
        ProxyAdBanner: true,
        GroupSelector: true,
        ModelWhitelistSelector: true,
        QuotaLimitCard: true,
        OAuthAuthorizationFlow: true,
        Select: true
      }
    }
  })
}

function platformTabButtons(wrapper: ReturnType<typeof mountModal>) {
  return wrapper.findAll('[data-tour="account-form-platform"] button')
}

function domesticProviderButtons(wrapper: ReturnType<typeof mountModal>) {
  return wrapper.findAll('[data-tour="account-form-domestic-provider"] button')
}

describe('CreateAccountModal domestic Coding Plan UI', () => {
  beforeEach(() => {
    createAccountMock.mockReset()
    createAccountMock.mockResolvedValue({ id: 1 })
  })

  it('shows exactly five top-level platform entries', () => {
    const wrapper = mountModal()
    const tabs = platformTabButtons(wrapper)
    expect(tabs).toHaveLength(5)
    const text = tabs.map((b) => b.text()).join('|')
    expect(text).toContain('Anthropic')
    expect(text).toContain('OpenAI')
    expect(text).toContain('Gemini')
    expect(text).toContain('Antigravity')
    expect(text).toContain('domesticTab')
  })

  it('reveals five domestic provider sub-tabs only after selecting the domestic entry', async () => {
    const wrapper = mountModal()
    expect(domesticProviderButtons(wrapper)).toHaveLength(0)

    const domesticTab = platformTabButtons(wrapper).find((b) => b.text().includes('domesticTab'))
    expect(domesticTab).toBeTruthy()
    await domesticTab!.trigger('click')

    const subTabs = domesticProviderButtons(wrapper)
    expect(subTabs).toHaveLength(5)
    const labels = subTabs.map((b) => b.text()).join('|')
    expect(labels).toContain('Kimi')
    expect(labels).toContain('Zhipu')
    expect(labels).toContain('MiniMax')
    expect(labels).toContain('Volcengine')
    expect(labels).toContain('MiMo')
  })

  it('no longer renders the API format radio or the quota base URL field', async () => {
    const wrapper = mountModal()
    const domesticTab = platformTabButtons(wrapper).find((b) => b.text().includes('domesticTab'))
    await domesticTab!.trigger('click')
    await wrapper.vm.$nextTick()

    const html = wrapper.html()
    expect(wrapper.find('input[type="radio"][value="chat_completions"]').exists()).toBe(false)
    expect(wrapper.find('input[type="radio"][value="anthropic_messages"]').exists()).toBe(false)
    expect(html).not.toContain('codingPlan.apiFormat')
    expect(html).not.toContain('codingPlan.quotaBaseUrl')
  })

  it('creates both the Chat/Codex and Claude Code accounts from a single key (kimi)', async () => {
    const wrapper = mountModal()
    const domesticTab = platformTabButtons(wrapper).find((b) => b.text().includes('domesticTab'))
    await domesticTab!.trigger('click')
    await wrapper.vm.$nextTick()

    // Fill the API key (kimi base url is prefilled by the platform watcher).
    const apiKeyInput = wrapper.find('input[type="password"]')
    expect(apiKeyInput.exists()).toBe(true)
    await apiKeyInput.setValue('sk-kimi-test')

    await wrapper.find('#create-account-form').trigger('submit')
    await new Promise((r) => setTimeout(r, 0))

    expect(createAccountMock).toHaveBeenCalledTimes(2)
    const names = createAccountMock.mock.calls.map((c) => c[0].name)
    expect(names.some((n: string) => n.endsWith('- Chat/Codex'))).toBe(true)
    expect(names.some((n: string) => n.endsWith('- Claude Code'))).toBe(true)

    const wireApis = createAccountMock.mock.calls.map((c) => c[0].credentials.wire_api)
    expect(wireApis).toContain('openai_chat')
    expect(wireApis).toContain('anthropic_messages')
  })

  it('creates only the Chat/Codex account for volcengine', async () => {
    const wrapper = mountModal()
    const domesticTab = platformTabButtons(wrapper).find((b) => b.text().includes('domesticTab'))
    await domesticTab!.trigger('click')
    await wrapper.vm.$nextTick()

    const volcTab = domesticProviderButtons(wrapper).find((b) => b.text().includes('Volcengine'))
    await volcTab!.trigger('click')
    await wrapper.vm.$nextTick()

    await wrapper.find('input[type="password"]').setValue('sk-volc-test')
    await wrapper.find('#create-account-form').trigger('submit')
    await new Promise((r) => setTimeout(r, 0))

    expect(createAccountMock).toHaveBeenCalledTimes(1)
    expect(createAccountMock.mock.calls[0][0].name).toContain('- Chat/Codex')
  })
})
