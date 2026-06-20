package service

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type codingPlanRoundTripFunc func(*http.Request) (*http.Response, error)

func (f codingPlanRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestParseKimiCodingPlanQuota(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)

	t.Run("limits and usage with ISO reset", func(t *testing.T) {
		snapshot, err := ParseKimiCodingPlanQuota([]byte(`{
			"limits":[{"detail":{"limit":100,"remaining":25,"resetTime":"2026-06-14T15:00:00Z"}}],
			"usage":{"limit":1000,"remaining":400,"resetTime":"2026-06-21T10:00:00Z"}
		}`), now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourUsedPercent)
		require.InDelta(t, 75, *snapshot.FiveHourUsedPercent, 0.001)
		require.NotNil(t, snapshot.WeeklyUsedPercent)
		require.InDelta(t, 60, *snapshot.WeeklyUsedPercent, 0.001)
		require.Equal(t, int64(5*60*60), *snapshot.FiveHourResetAfterSeconds)
	})

	t.Run("seconds reset timestamp", func(t *testing.T) {
		reset := now.Add(2 * time.Hour).Unix()
		body := []byte(`{"limits":[{"detail":{"limit":10,"remaining":8,"resetTime":` + int64String(reset) + `}}]}`)
		snapshot, err := ParseKimiCodingPlanQuota(body, now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourResetAt)
		require.Equal(t, reset, snapshot.FiveHourResetAt.Unix())
	})

	t.Run("milliseconds reset timestamp", func(t *testing.T) {
		reset := now.Add(3 * time.Hour).UnixMilli()
		body := []byte(`{"limits":[{"detail":{"limit":10,"remaining":5,"resetTime":` + int64String(reset) + `}}]}`)
		snapshot, err := ParseKimiCodingPlanQuota(body, now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourResetAt)
		require.Equal(t, reset, snapshot.FiveHourResetAt.UnixMilli())
	})

	t.Run("only five hour", func(t *testing.T) {
		snapshot, err := ParseKimiCodingPlanQuota([]byte(`{"limits":[{"detail":{"limit":50,"remaining":0}}]}`), now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourUsedPercent)
		require.Nil(t, snapshot.WeeklyUsedPercent)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		_, err := ParseKimiCodingPlanQuota([]byte(`{"limits":[`), now)
		require.Error(t, err)
	})

	t.Run("HTTP error snapshots", func(t *testing.T) {
		auth := parseCodingPlanHTTPError(CodingPlanProviderKimi, 401, nil)
		require.True(t, auth.CredentialExpired)
		require.Equal(t, "credential_expired", *auth.AccountStatus)

		limited := parseCodingPlanHTTPError(CodingPlanProviderKimi, 429, nil)
		require.True(t, limited.TemporaryUnschedulableHint)
	})
}

func TestParseZhipuCodingPlanQuota(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)

	t.Run("unit maps windows without reset sorting", func(t *testing.T) {
		snapshot, err := ParseZhipuCodingPlanQuota([]byte(`{
			"success":true,
			"data":{"level":"pro","limits":[
				{"type":"TOKENS_LIMIT","unit":6,"percentage":80,"nextResetTime":"2026-06-14T11:00:00Z"},
				{"type":"TOKENS_LIMIT","unit":3,"percentage":20}
			]}
		}`), now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourUsedPercent)
		require.Equal(t, 20.0, *snapshot.FiveHourUsedPercent)
		require.Nil(t, snapshot.FiveHourResetAt)
		require.NotNil(t, snapshot.WeeklyUsedPercent)
		require.Equal(t, 80.0, *snapshot.WeeklyUsedPercent)
		require.Equal(t, "pro", *snapshot.PlanName)
	})

	t.Run("legacy single token limit becomes five hour", func(t *testing.T) {
		snapshot, err := ParseZhipuCodingPlanQuota([]byte(`{
			"success":true,
			"data":{"limits":[{"type":"TOKENS_LIMIT","percentage":33,"nextResetTime":"2026-06-14T12:00:00Z"}]}
		}`), now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourUsedPercent)
		require.Equal(t, 33.0, *snapshot.FiveHourUsedPercent)
		require.Nil(t, snapshot.WeeklyUsedPercent)
	})

	t.Run("business error", func(t *testing.T) {
		snapshot, err := ParseZhipuCodingPlanQuota([]byte(`{"success":false,"message":"bad quota"}`), now)
		require.Error(t, err)
		require.False(t, snapshot.Success)
		require.Contains(t, snapshot.ErrorMessage, "bad quota")
	})

	t.Run("Authorization header has no Bearer prefix", func(t *testing.T) {
		var gotAuth string
		client := &http.Client{Transport: codingPlanRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			gotAuth = req.Header.Get("Authorization")
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(`{"success":true,"data":{"limits":[]}}`)),
				Header:     make(http.Header),
			}, nil
		})}
		probe := NewZhipuCodingPlanProbe(client)
		account := &Account{
			Platform: PlatformOpenAI,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "zhipu-key",
				"base_url": "https://open.bigmodel.cn/api/paas/v4",
			},
		}
		_, err := probe.Probe(t.Context(), account)
		require.NoError(t, err)
		require.Equal(t, "zhipu-key", gotAuth)
	})
}

func TestParseMiniMaxCodingPlanQuota(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)

	t.Run("general model with weekly status", func(t *testing.T) {
		snapshot, err := ParseMiniMaxCodingPlanQuota([]byte(`{
			"base_resp":{"status_code":0},
			"model_remains":[
				{"model_name":"video","current_interval_remaining_percent":1},
				{"model_name":"general","current_interval_remaining_percent":25,"end_time":1781438400,"current_weekly_status":1,"current_weekly_remaining_percent":40,"weekly_end_time":1782043200000}
			]
		}`), now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourUsedPercent)
		require.Equal(t, 75.0, *snapshot.FiveHourUsedPercent)
		require.NotNil(t, snapshot.WeeklyUsedPercent)
		require.Equal(t, 60.0, *snapshot.WeeklyUsedPercent)
	})

	t.Run("weekly status absent", func(t *testing.T) {
		snapshot, err := ParseMiniMaxCodingPlanQuota([]byte(`{
			"base_resp":{"status_code":0},
			"model_remains":[{"model_name":"general","current_interval_remaining_percent":90,"current_weekly_status":3,"current_weekly_remaining_percent":1}]
		}`), now)
		require.NoError(t, err)
		require.NotNil(t, snapshot.FiveHourUsedPercent)
		require.Equal(t, 10.0, *snapshot.FiveHourUsedPercent)
		require.Nil(t, snapshot.WeeklyUsedPercent)
	})

	t.Run("business error", func(t *testing.T) {
		snapshot, err := ParseMiniMaxCodingPlanQuota([]byte(`{"base_resp":{"status_code":1001,"status_msg":"denied"}}`), now)
		require.Error(t, err)
		require.False(t, snapshot.Success)
		require.Equal(t, "denied", snapshot.ErrorMessage)
	})

	t.Run("endpoint selection", func(t *testing.T) {
		require.Equal(t, "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains", miniMaxCodingPlanQuotaEndpoint("https://api.minimaxi.com"))
		require.Equal(t, "https://api.minimax.io/v1/api/openplatform/coding_plan/remains", miniMaxCodingPlanQuotaEndpoint("https://api.minimax.io"))
	})
}

func TestVolcengineCodingPlanQuotaProbe(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)

	t.Run("parse remaining percent windows", func(t *testing.T) {
		snapshot, err := ParseVolcengineCodingPlanQuota([]byte(`{
			"ResponseMetadata":{"RequestId":"req"},
			"Result":{
				"PlanName":"Coding Pro",
				"SeatInfoUsages":[{
					"SeatID":"seat-1",
					"CurrentIntervalRemainingPercent":25,
					"CurrentIntervalEndTime":1781438400,
					"CurrentWeeklyRemainingPercent":40,
					"WeeklyEndTime":1782043200000
				}]
			}
		}`), now, "ListSeatInfoUsages")
		require.NoError(t, err)
		require.True(t, snapshot.Success)
		require.NotNil(t, snapshot.FiveHourUsedPercent)
		require.Equal(t, 75.0, *snapshot.FiveHourUsedPercent)
		require.NotNil(t, snapshot.WeeklyUsedPercent)
		require.Equal(t, 60.0, *snapshot.WeeklyUsedPercent)
		require.Equal(t, "Coding Pro", *snapshot.PlanName)
		require.Equal(t, "ListSeatInfoUsages", snapshot.Raw["volcengine_action"])
	})

	t.Run("active probe is unsupported without a verified public endpoint", func(t *testing.T) {
		probe := NewVolcengineCodingPlanProbe(nil)
		account := &Account{
			Platform: string(CodingPlanProviderVolcengine),
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"api_key":  "ark-test",
				"base_url": "https://ark.cn-beijing.volces.com/api/coding/v3",
			},
			Extra: map[string]any{
				"coding_plan_provider": "volcengine",
			},
		}
		snapshot, err := probe.Probe(t.Context(), account)
		require.ErrorIs(t, err, ErrCodingPlanQuotaProbeUnsupported)
		require.False(t, snapshot.Success)
		require.Equal(t, CodingPlanProbeStatusUnsupported, snapshot.QuotaProbeStatus)
	})
}

func TestCodingPlanQuotaSchedulingHelpers(t *testing.T) {
	now := time.Now()
	resetAt := now.Add(time.Hour).Format(time.RFC3339)
	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"coding_plan_provider":         "minimax",
			"coding_plan_5h_used_percent":  99.0,
			"coding_plan_5h_reset_at":      resetAt,
			"coding_plan_usage_updated_at": now.Add(-30 * time.Minute).Format(time.RFC3339),
		},
	}
	paused, reason := shouldAutoPauseCodingPlanAccountByQuota(t.Context(), account)
	require.True(t, paused)
	require.Equal(t, "5h", reason.window)

	account.Extra["coding_plan_usage_updated_at"] = now.Add(-3 * time.Hour).Format(time.RFC3339)
	require.Equal(t, 0.0, codingPlanQuotaUtilization(account, now))

	decision := ClassifyCodingPlanProviderError(CodingPlanProviderMiniMax, 429, nil, []byte(`{"error":"rate"}`), account)
	require.True(t, decision.ShouldFailover)
	require.True(t, decision.RateLimited)
	require.NotNil(t, decision.TempUnschedulableUntil)

	quota := ClassifyCodingPlanProviderError(CodingPlanProviderVolcengine, 400, nil, []byte(`{"error":"AFP exhausted"}`), account)
	require.True(t, quota.QuotaExhausted)
	require.True(t, quota.ShouldFailover)
}

func TestCodingPlanProviderDetectionAndUnsupportedProbe(t *testing.T) {
	require.Equal(t, CodingPlanProviderKimi, DetectCodingPlanProviderFromBaseURL("https://api.kimi.com/coding/v1"))
	require.Equal(t, CodingPlanProviderZhipu, DetectCodingPlanProviderFromBaseURL("https://open.bigmodel.cn/api/paas/v4"))
	require.Equal(t, CodingPlanProviderZhipu, DetectCodingPlanProviderFromBaseURL("https://api.z.ai/api/paas/v4"))
	require.Equal(t, CodingPlanProviderMiniMax, DetectCodingPlanProviderFromBaseURL("https://api.minimax.io/v1"))
	require.Equal(t, CodingPlanProviderVolcengine, DetectCodingPlanProviderFromBaseURL("https://ark.cn-beijing.volces.com/api/v3"))
	require.Equal(t, CodingPlanProviderMiMo, DetectCodingPlanProviderFromBaseURL("https://mimo.api.xiaomi.com/v1"))
	require.Empty(t, DetectCodingPlanProviderFromBaseURL("https://www.mi.com/shop"))
	require.Empty(t, DetectCodingPlanProviderFromBaseURL("https://openai-compatible.example.com/v1"))

	require.Equal(t, CodingPlanProviderKimi, ResolveCodingPlanProvider(&Account{
		Platform: string(CodingPlanProviderKimi),
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"base_url": "https://proxy.example.com/v1",
		},
		Extra: map[string]any{},
	}))

	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			"coding_plan_provider": "volcengine",
		},
	}
	snapshot, err := ProbeCodingPlanQuota(t.Context(), account)
	require.ErrorIs(t, err, ErrCodingPlanQuotaProbeUnsupported)
	require.NotNil(t, snapshot)
	require.Equal(t, CodingPlanProbeStatusUnsupported, snapshot.QuotaProbeStatus)
	require.False(t, snapshot.Success)
	require.Contains(t, snapshot.ErrorMessage, "no verified public endpoint")

	account.Extra["coding_plan_provider"] = "mimo"
	snapshot, err = ProbeCodingPlanQuota(t.Context(), account)
	require.ErrorIs(t, err, ErrCodingPlanQuotaProbeUnsupported)
	require.NotNil(t, snapshot)
	require.Equal(t, CodingPlanProbeStatusUnsupported, snapshot.QuotaProbeStatus)
	require.False(t, snapshot.Success)
}

func TestCodingPlanQuotaPauseRecovery(t *testing.T) {
	now := time.Date(2026, 6, 14, 10, 0, 0, 0, time.UTC)
	pastReset := now.Add(-time.Minute).Format(time.RFC3339)
	staleUpdated := now.Add(-3 * time.Hour).Format(time.RFC3339)

	account := &Account{
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Extra: map[string]any{
			"coding_plan_provider":         "kimi",
			"coding_plan_5h_used_percent":  100.0,
			"coding_plan_5h_reset_at":      pastReset,
			"coding_plan_usage_updated_at": now.Add(-30 * time.Minute).Format(time.RFC3339),
		},
	}
	utilization, ok := resolveCodingPlanQuotaUtilization(account.Extra, "5h", now)
	require.False(t, ok)
	require.Equal(t, 0.0, utilization)

	account.Extra["coding_plan_5h_reset_at"] = now.Add(time.Hour).Format(time.RFC3339)
	account.Extra["coding_plan_usage_updated_at"] = staleUpdated
	utilization, ok = resolveCodingPlanQuotaUtilization(account.Extra, "5h", now)
	require.False(t, ok)
	require.Equal(t, 0.0, utilization)
	require.Equal(t, 0.0, codingPlanQuotaUtilization(account, now))
}

func TestCodingPlanAPIFormatDetectionUsesWireAndResponsesSupport(t *testing.T) {
	account := &Account{
		Platform: "kimi",
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_format": "chat_completions",
		},
		Extra: map[string]any{
			"coding_plan_provider": "kimi",
		},
	}
	require.True(t, IsCodingPlanChatCompletionsAccount(account))
	require.False(t, IsCodingPlanAnthropicMessagesAccount(account))

	account.Credentials = map[string]any{
		"wire_api": "anthropic_messages",
	}
	require.False(t, IsCodingPlanChatCompletionsAccount(account))
	require.True(t, IsCodingPlanAnthropicMessagesAccount(account))

	account.Credentials = map[string]any{}
	account.Extra["responses_support"] = "via_anthropic_messages"
	require.False(t, IsCodingPlanChatCompletionsAccount(account))
	require.True(t, IsCodingPlanAnthropicMessagesAccount(account))
}

func TestCodingPlanProviderErrorClassifier(t *testing.T) {
	account := &Account{Extra: map[string]any{}}
	auth := ClassifyCodingPlanProviderError(CodingPlanProviderKimi, 403, nil, []byte(`{"error":"forbidden"}`), account)
	require.True(t, auth.AuthFailed)
	require.False(t, auth.ShouldFailover)

	overloaded := ClassifyCodingPlanProviderError(CodingPlanProviderZhipu, 529, nil, []byte(`overloaded`), account)
	require.True(t, overloaded.Overloaded)
	require.True(t, overloaded.ShouldFailover)
	require.NotNil(t, overloaded.TempUnschedulableUntil)

	serverError := ClassifyCodingPlanProviderError(CodingPlanProviderMiniMax, 503, nil, []byte(`service unavailable`), account)
	require.True(t, serverError.Overloaded)
	require.True(t, serverError.Retryable)

	weeklyReset := time.Now().Add(2 * time.Hour).UTC()
	account.Extra["coding_plan_weekly_reset_at"] = weeklyReset.Format(time.RFC3339)
	weeklyQuota := ClassifyCodingPlanProviderError(CodingPlanProviderKimi, 400, nil, []byte(`weekly quota exhausted`), account)
	require.True(t, weeklyQuota.QuotaExhausted)
	require.NotNil(t, weeklyQuota.TempUnschedulableUntil)
	require.WithinDuration(t, weeklyReset, *weeklyQuota.TempUnschedulableUntil, time.Second)
}

func int64String(v int64) string {
	return strconv.FormatInt(v, 10)
}
