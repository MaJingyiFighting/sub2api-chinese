package service

import (
	"fmt"
	"net/url"
	"strings"
)

func IsCodingPlanPlatform(platform string) bool {
	switch platform {
	case string(CodingPlanProviderKimi),
		string(CodingPlanProviderZhipu),
		string(CodingPlanProviderMiniMax),
		string(CodingPlanProviderVolcengine),
		string(CodingPlanProviderMiMo):
		return true
	default:
		return false
	}
}

func IsPureKeyDomesticPlatform(platform string) bool {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case PlatformDeepSeek, PlatformCustomOpenAICompatible, PlatformCustomAnthropicCompatible:
		return true
	default:
		return false
	}
}

func IsDomesticAggregatePlatform(platform string) bool {
	return strings.TrimSpace(platform) == PlatformDomestic
}

func IsCodexResponsesViaChatPlatform(platform string) bool {
	platform = strings.TrimSpace(platform)
	return IsCodingPlanPlatform(platform) ||
		IsDomesticAggregatePlatform(platform) ||
		platform == PlatformDeepSeek ||
		platform == PlatformCustomOpenAICompatible
}

func ResolveCodingPlanProvider(account *Account) CodingPlanProvider {
	if account == nil {
		return ""
	}
	if provider := normalizeCodingPlanProvider(account.GetExtraString("coding_plan_provider")); provider != "" {
		return provider
	}
	if provider := DetectCodingPlanProviderFromBaseURL(accountCodingPlanBaseURL(account)); provider != "" {
		return provider
	}
	return normalizeCodingPlanProvider(account.Platform)
}

func DetectCodingPlanProviderFromBaseURL(baseURL string) CodingPlanProvider {
	normalized := strings.ToLower(strings.TrimSpace(baseURL))
	if normalized == "" {
		return ""
	}
	parseTarget := normalized
	if !strings.Contains(parseTarget, "://") {
		parseTarget = "https://" + parseTarget
	}
	parsed, err := url.Parse(parseTarget)
	host := ""
	path := ""
	if err == nil && parsed != nil {
		host = strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
		path = strings.ToLower(parsed.EscapedPath())
	}
	if host == "" {
		host = normalized
	}
	target := host + path
	switch {
	case host == "api.kimi.com" && strings.Contains(path, "/coding"), host == "api.moonshot.cn":
		return CodingPlanProviderKimi
	case host == "open.bigmodel.cn", strings.HasSuffix(host, ".bigmodel.cn"), host == "api.z.ai":
		return CodingPlanProviderZhipu
	case host == "api.minimaxi.com", host == "api.minimax.io":
		return CodingPlanProviderMiniMax
	case strings.HasSuffix(host, ".volces.com"), host == "volces.com", strings.Contains(host, "volcengine"):
		return CodingPlanProviderVolcengine
	case strings.Contains(target, "mimo"), strings.Contains(host, "xiaomi") && (strings.Contains(host, "api") || strings.Contains(path, "mimo")):
		return CodingPlanProviderMiMo
	default:
		return ""
	}
}

func normalizeCodingPlanProvider(value string) CodingPlanProvider {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(CodingPlanProviderKimi), "moonshot", "kimi_for_coding":
		return CodingPlanProviderKimi
	case string(CodingPlanProviderZhipu), "glm", "bigmodel", "zai", "z.ai":
		return CodingPlanProviderZhipu
	case string(CodingPlanProviderMiniMax), "mini-max", "minimax-coding":
		return CodingPlanProviderMiniMax
	case string(CodingPlanProviderVolcengine), "volcano", "ark", "doubao":
		return CodingPlanProviderVolcengine
	case string(CodingPlanProviderMiMo), "xiaomi":
		return CodingPlanProviderMiMo
	default:
		return ""
	}
}

func IsCodingPlanAccount(account *Account) bool {
	return ResolveCodingPlanProvider(account) != ""
}

// ValidateCodingPlanAccountConsistency enforces that a domestic Coding Plan
// account is configured coherently. It is invoked by create/update/bulk-update
// on the final merged state so partial updates cannot bypass the rule.
func ValidateCodingPlanAccountConsistency(platform, typ string, credentials, extra map[string]any) error {
	platform = strings.ToLower(strings.TrimSpace(platform))
	typ = strings.ToLower(strings.TrimSpace(typ))
	if provider := DetectCodingPlanProviderFromBaseURL(codingPlanMapString(credentials, "base_url")); provider != "" &&
		platform == PlatformOpenAI && (typ == "" || typ == AccountTypeAPIKey || typ == AccountTypeUpstream) {
		return fmt.Errorf("base_url belongs to %s Coding Plan; use platform=%s instead of platform=openai", provider, provider)
	}
	if extra == nil {
		return nil
	}
	rawProvider, ok := extra["coding_plan_provider"].(string)
	if !ok {
		return nil
	}
	provider := strings.ToLower(strings.TrimSpace(rawProvider))
	if provider == "" {
		return nil
	}
	if !IsCodingPlanPlatform(provider) {
		return fmt.Errorf("unsupported coding_plan_provider %q (allowed: kimi, zhipu, minimax, volcengine, mimo)", rawProvider)
	}
	if !IsCodingPlanPlatform(platform) {
		return fmt.Errorf("coding plan accounts must use one of the domestic platforms (kimi/zhipu/minimax/volcengine/mimo); got platform=%q", platform)
	}
	if platform != provider {
		return fmt.Errorf("coding_plan_provider %q does not match platform %q; they must be the same", provider, platform)
	}
	if typ != AccountTypeAPIKey && typ != AccountTypeUpstream {
		return fmt.Errorf("coding plan accounts must use 'apikey' or 'upstream' type; got type=%q", typ)
	}
	return nil
}

func codingPlanMapString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	raw, ok := m[key]
	if !ok || raw == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(raw))
}

func mergeCodingPlanValidationMap(base, updates map[string]any) map[string]any {
	if len(updates) == 0 {
		return base
	}
	merged := make(map[string]any, len(base)+len(updates))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range updates {
		merged[k] = v
	}
	return merged
}
