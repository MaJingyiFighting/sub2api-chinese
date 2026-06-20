package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDomesticAccountCarriesBothNativeProtocolEndpoints(t *testing.T) {
	account := &Account{
		Platform: string(CodingPlanProviderKimi),
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":            "https://api.kimi.com/coding/v1",
			"anthropic_base_url":  "https://api.kimi.com/coding",
			"anthropic_auth_mode": "x-api-key",
			"api_key":             "secret",
		},
	}

	require.Equal(t, "https://api.kimi.com/coding/v1", account.GetOpenAIBaseURL())
	require.Equal(t, "https://api.kimi.com/coding", account.GetBaseURL())
	require.True(t, AccountSupportsNativeChatCompletions(account))
	require.True(t, AccountSupportsNativeAnthropicMessages(account))
	require.True(t, account.IsAnthropicAPIKeyPassthroughEnabled())
	require.True(t, IsAccountCompatibleWithSchedulingPlatform(account, PlatformDomestic))
	require.True(t, IsAccountCompatibleWithSchedulingPlatform(account, PlatformAnthropic))
	require.False(t, IsAccountCompatibleWithSchedulingPlatform(account, PlatformOpenAI))
	require.Equal(t, "x-api-key", account.GetAnthropicAPIKeyAuthMode())
}

func TestPureKeyDomesticAccountUsesNativeChatCredential(t *testing.T) {
	account := &Account{
		Platform: PlatformDeepSeek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url":           "https://api.deepseek.com",
			"anthropic_base_url": "https://api.deepseek.com/anthropic",
			"api_key":            "deepseek-key",
		},
	}

	require.Equal(t, "deepseek-key", account.GetOpenAIApiKey())
	require.Equal(t, "https://api.deepseek.com", account.GetOpenAIBaseURL())
	require.Equal(t, "https://api.deepseek.com/anthropic", account.GetBaseURL())
}
