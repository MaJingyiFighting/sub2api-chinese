package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Task D1: 429 cooldown derives from Retry-After (delta-seconds).
func TestClassifyCodingPlan429_RetryAfterSeconds(t *testing.T) {
	now := time.Now()
	headers := http.Header{"Retry-After": []string{"120"}}
	d := ClassifyCodingPlanProviderError(CodingPlanProviderKimi, 429, headers, []byte(`{"error":"rate limited"}`), &Account{Extra: map[string]any{}})

	require.True(t, d.RateLimited)
	require.True(t, d.ShouldFailover)
	require.Equal(t, "rate_limited", d.Reason)
	require.NotNil(t, d.TempUnschedulableUntil)
	delay := d.TempUnschedulableUntil.Sub(now)
	require.InDelta(t, (120 * time.Second).Seconds(), delay.Seconds(), 3)
}

// Task D1: 429 cooldown derives from an HTTP-date Retry-After.
func TestClassifyCodingPlan429_RetryAfterHTTPDate(t *testing.T) {
	now := time.Now()
	target := now.Add(90 * time.Second).UTC()
	headers := http.Header{"Retry-After": []string{target.Format(http.TimeFormat)}}
	d := ClassifyCodingPlanProviderError(CodingPlanProviderZhipu, 429, headers, nil, &Account{Extra: map[string]any{}})

	require.NotNil(t, d.TempUnschedulableUntil)
	delay := d.TempUnschedulableUntil.Sub(now)
	// HTTP-date has second resolution; allow a few seconds slack.
	require.InDelta(t, (90 * time.Second).Seconds(), delay.Seconds(), 3)
}

// Task D1: provider x-ratelimit-reset header (delta-seconds) is honored.
func TestClassifyCodingPlan429_XRateLimitReset(t *testing.T) {
	now := time.Now()
	headers := http.Header{"X-Ratelimit-Reset": []string{"75"}}
	d := ClassifyCodingPlanProviderError(CodingPlanProviderMiniMax, 429, headers, nil, &Account{Extra: map[string]any{}})

	require.NotNil(t, d.TempUnschedulableUntil)
	delay := d.TempUnschedulableUntil.Sub(now)
	require.InDelta(t, (75 * time.Second).Seconds(), delay.Seconds(), 3)
}

// Task D1: no headers → default 60s, then exponential backoff 1m/2m/5m/10m/15m.
func TestClassifyCodingPlan429_DefaultAndExponentialBackoff(t *testing.T) {
	now := time.Now()
	account := &Account{Extra: map[string]any{}}

	expect := []time.Duration{60 * time.Second, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute, 15 * time.Minute, 15 * time.Minute}
	for i, want := range expect {
		d := ClassifyCodingPlanProviderError(CodingPlanProviderMiMo, 429, nil, nil, account)
		require.NotNil(t, d.TempUnschedulableUntil, "step %d", i)
		delay := d.TempUnschedulableUntil.Sub(now)
		require.InDelta(t, want.Seconds(), delay.Seconds(), 3, "backoff step %d (streak %d)", i, d.RateLimitStreak)
		require.Equal(t, i+1, d.RateLimitStreak)
		// Simulate persistence so the next call escalates.
		account.Extra["coding_plan_rate_limit_streak"] = d.RateLimitStreak
		account.Extra["coding_plan_rate_limit_at"] = time.Now().UTC().Format(time.RFC3339)
	}
}

// Task D1: a huge Retry-After is clamped to the 15-minute ceiling.
func TestClassifyCodingPlan429_CapsAt15Minutes(t *testing.T) {
	now := time.Now()
	headers := http.Header{"Retry-After": []string{"99999"}}
	d := ClassifyCodingPlanProviderError(CodingPlanProviderKimi, 429, headers, nil, &Account{Extra: map[string]any{}})

	require.NotNil(t, d.TempUnschedulableUntil)
	delay := d.TempUnschedulableUntil.Sub(now)
	require.InDelta(t, (15 * time.Minute).Seconds(), delay.Seconds(), 3)
}

// Task D1: the consecutive-429 streak resets after a long quiet window.
func TestNextCodingPlanRateLimitStreak_ResetsAfterQuietWindow(t *testing.T) {
	now := time.Now()
	account := &Account{Extra: map[string]any{
		"coding_plan_rate_limit_streak": 4,
		"coding_plan_rate_limit_at":     now.Add(-30 * time.Minute).Format(time.RFC3339),
	}}
	require.Equal(t, 1, nextCodingPlanRateLimitStreak(account, now))

	account.Extra["coding_plan_rate_limit_at"] = now.Add(-1 * time.Minute).Format(time.RFC3339)
	require.Equal(t, 5, nextCodingPlanRateLimitStreak(account, now))
}

// Task D3: shouldFailoverUpstreamError covers 429/500/502/503/504/529/401.
func TestGatewayShouldFailoverUpstreamError_CoversTransientCodes(t *testing.T) {
	s := &GatewayService{}
	for _, code := range []int{401, 429, 500, 502, 503, 504, 529} {
		require.True(t, s.shouldFailoverUpstreamError(code), "status %d should failover", code)
	}
	// A plain bad-request must NOT blindly failover.
	require.False(t, s.shouldFailoverUpstreamError(400))
}

// Task D4: a coding-plan account with no quota snapshot and no explicit quota
// limits must remain schedulable (quota-unknown must not block dispatch).
func TestCodingPlanMissingQuotaDoesNotBlockScheduling(t *testing.T) {
	account := &Account{
		ID:          7,
		Platform:    string(CodingPlanProviderVolcengine),
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key":  "sk-volc",
			"base_url": "https://ark.cn-beijing.volces.com/api/v3",
		},
		Extra: map[string]any{
			"coding_plan_provider": "volcengine",
		},
	}
	require.False(t, account.IsQuotaExceeded())
	require.True(t, account.IsSchedulable())
}
