package service

import (
	"encoding/json"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// ShouldRouteCodexResponsesToChat determines if a Codex Responses request should be routed
// to a Chat-only upstream using the codex-responses-to-chat conversion.
func ShouldRouteCodexResponsesToChat(account *Account, channel *Channel, endpoint string, body []byte) bool {
	validEndpoints := []string{
		"/responses",
		"/v1/responses",
		"/responses/compact",
		"/v1/responses/compact",
		"/backend-api/codex/responses",
		"/backend-api/codex/responses/compact",
	}
	isValidEndpoint := false
	for _, e := range validEndpoints {
		if strings.HasSuffix(endpoint, e) {
			isValidEndpoint = true
			break
		}
	}
	if !isValidEndpoint {
		return false
	}

	// OpenAI OAuth accounts do not use Chat Completions route
	if account != nil && account.Type != AccountTypeAPIKey {
		return false
	}

	// If explicit response ID is requested, we don't route to stateless Chat Completions
	// unless we implement local response history
	if len(body) > 0 {
		previousResponseID := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
		if previousResponseID != "" {
			return false
		}
	}

	wireApi := getAccountOrChannelExtraString(account, channel, "wire_api")
	responsesSupport := getAccountOrChannelExtraString(account, channel, "responses_support")
	chatRouteEnabled := getAccountOrChannelExtraBool(account, channel, "chat_completions_route_enabled")

	openaiCapResponses := true
	if account != nil && account.Credentials != nil {
		if capsRaw, exists := account.Credentials["openai_capabilities"]; exists {
			capsJSON, _ := json.Marshal(capsRaw)
			respCap := gjson.GetBytes(capsJSON, "responses")
			if respCap.Exists() && !respCap.Bool() {
				openaiCapResponses = false
			}
		}
	}

	apiFormat := getAccountOrChannelExtraString(account, channel, "api_format")
	if apiFormat == "openai_chat" {
		return true
	}
	if wireApi == "chat" {
		return true
	}
	if responsesSupport == "unsupported" {
		return true
	}
	if !openaiCapResponses {
		return true
	}
	if chatRouteEnabled {
		return true
	}

	baseURL := ""
	if account != nil && account.Credentials != nil {
		if url, ok := account.Credentials["base_url"].(string); ok {
			baseURL = url
		}
	}
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return true
	}

	// Check if supplier model explicitly chat-only
	// e.g. deepseek, minimax, kimi, etc. We can look at base_url.
	if strings.Contains(baseURL, "api.deepseek.com") ||
		strings.Contains(baseURL, "api.minimaxi.com") ||
		strings.Contains(baseURL, "api.moonshot.cn") {
		// Assuming we want to automatically route if missing config,
		// though user suggests "unless explicit". Let's enable by default for known chat-only.
		if responsesSupport == "supported" {
			return false
		}
		return true
	}

	return false
}

func getAccountOrChannelExtraString(account *Account, channel *Channel, key string) string {
	if account != nil && account.Extra != nil {
		if v, ok := account.Extra[key].(string); ok && v != "" {
			return v
		}
	}
	if channel != nil && channel.FeaturesConfig != nil {
		if v, ok := channel.FeaturesConfig[key].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func getAccountOrChannelExtraBool(account *Account, channel *Channel, key string) bool {
	if account != nil && account.Extra != nil {
		if v, ok := account.Extra[key].(bool); ok {
			return v
		}
	}
	if channel != nil && channel.FeaturesConfig != nil {
		if v, ok := channel.FeaturesConfig[key].(bool); ok {
			return v
		}
	}
	return false
}

func codexBoolPtr(b bool) *bool {
	return &b
}

// GetCodexChatReasoningConfig returns the reasoning configuration for an account/channel,
// applying built-in defaults for known providers.
func GetCodexChatReasoningConfig(account *Account, channel *Channel) apicompat.CodexChatReasoningConfig {
	var cfg apicompat.CodexChatReasoningConfig

	// Attempt to read explicit config
	if account != nil && account.Extra != nil {
		if raw, ok := account.Extra["codex_chat_reasoning"]; ok {
			if b, err := json.Marshal(raw); err == nil {
				_ = json.Unmarshal(b, &cfg)
			}
		}
	} else if channel != nil && channel.FeaturesConfig != nil {
		if raw, ok := channel.FeaturesConfig["codex_chat_reasoning"]; ok {
			if b, err := json.Marshal(raw); err == nil {
				_ = json.Unmarshal(b, &cfg)
			}
		}
	}

	// Apply defaults based on known providers if missing
	baseURL := ""
	if account != nil && account.Credentials != nil {
		if url, ok := account.Credentials["base_url"].(string); ok {
			baseURL = url
		}
	}

	if strings.Contains(baseURL, "api.deepseek.com") {
		if cfg.ThinkingParam == "" {
			cfg.ThinkingParam = "thinking"
		}
		if cfg.OutputFormat == "" {
			cfg.OutputFormat = "reasoning_content"
		}
		if cfg.SupportsThinking == nil {
			cfg.SupportsThinking = codexBoolPtr(true)
		}
		if cfg.SupportsEffort == nil {
			cfg.SupportsEffort = codexBoolPtr(true)
		}
	} else if strings.Contains(baseURL, "api.moonshot.cn") {
		if cfg.ThinkingParam == "" {
			cfg.ThinkingParam = "thinking"
		}
		if cfg.OutputFormat == "" {
			cfg.OutputFormat = "reasoning_content"
		}
		if cfg.SupportsThinking == nil {
			cfg.SupportsThinking = codexBoolPtr(true)
		}
		if cfg.SupportsEffort == nil {
			cfg.SupportsEffort = codexBoolPtr(false)
		}
	} else if strings.Contains(baseURL, "api.minimaxi.com") {
		if cfg.ThinkingParam == "" {
			cfg.ThinkingParam = "reasoning_split"
		}
		if cfg.OutputFormat == "" {
			cfg.OutputFormat = "reasoning_details"
		}
		if cfg.SupportsThinking == nil {
			cfg.SupportsThinking = codexBoolPtr(true)
		}
		if cfg.SupportsEffort == nil {
			cfg.SupportsEffort = codexBoolPtr(false)
		}
	} else if strings.Contains(baseURL, "openrouter.ai") {
		if cfg.ThinkingParam == "" {
			cfg.ThinkingParam = "none"
		}
		if cfg.OutputFormat == "" {
			cfg.OutputFormat = "auto"
		}
	}

	return cfg
}
