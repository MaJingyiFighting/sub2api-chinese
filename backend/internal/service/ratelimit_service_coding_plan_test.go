//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func newCodingPlanKimiAccount() *Account {
	return &Account{
		ID:          1,
		Platform:    string(CodingPlanProviderKimi),
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key":  "sk-kimi",
			"base_url": "https://api.moonshot.cn/v1",
		},
		Extra: map[string]any{
			"coding_plan_provider": "kimi",
		},
	}
}

// Task D1: a 429 from a domestic Coding Plan upstream marks the account
// temp-unschedulable, honoring Retry-After and persisting the 429 streak.
func TestRateLimitService_HandleUpstreamError_CodingPlan429TempUnschedulable(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := newCodingPlanKimiAccount()
	now := time.Now()

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, 429,
		http.Header{"Retry-After": []string{"120"}}, []byte(`{"error":"rate limited"}`))

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, 0, repo.setErrorCalls)
	require.InDelta(t, (120 * time.Second).Seconds(), repo.lastTempUntil.Sub(now).Seconds(), 5)
	require.Contains(t, repo.lastTempReason, "rate_limited")
	require.Equal(t, 1, repo.lastExtra["coding_plan_rate_limit_streak"])
}

func TestRateLimitService_HandleUpstreamError_PlatformFallbackWithoutExtra(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := &Account{
		ID:          11,
		Platform:    string(CodingPlanProviderKimi),
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key":  "sk-kimi",
			"base_url": "https://proxy.example.com/v1",
		},
		Extra: map[string]any{},
	}

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, 429,
		http.Header{"Retry-After": []string{"30"}}, []byte(`{"error":"rate limited"}`))

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.tempCalls)
	require.Equal(t, "kimi", repo.lastExtra["coding_plan_provider"])
}

// Task D2: a 401 from a domestic Coding Plan upstream disables the account
// (auth invalid) via SetError, so it is no longer scheduled.
func TestRateLimitService_HandleUpstreamError_CodingPlan401Disables(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	svc := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	account := newCodingPlanKimiAccount()

	shouldDisable := svc.HandleUpstreamError(context.Background(), account, 401,
		http.Header{}, []byte(`{"error":"invalid api key"}`))

	require.True(t, shouldDisable)
	require.Equal(t, 1, repo.setErrorCalls)
	require.Equal(t, 0, repo.tempCalls)
	require.Contains(t, strings.ToLower(repo.lastErrorMsg), "credential")
}

// Task D1 (integration): a 429 from the Chat Completions upstream signals
// failover AND blocks the account so the scheduler will skip it next time.
func TestForwardCodexResponsesViaCC_429BlocksAccountAndFailsOver(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"120"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"rate limited"}}`)),
	}}
	svc := &GatewayService{httpUpstream: upstream, rateLimitService: rl}
	body := []byte(`{"model":"kimi","stream":false,"input":"hi"}`)
	c, _ := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanKimiAccount(), body, nil)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusTooManyRequests, failoverErr.StatusCode)
	require.Equal(t, 1, repo.tempCalls, "429 should mark the account temp-unschedulable")
}

// Task D2 (integration): a 401 from the Chat Completions upstream signals
// failover AND disables the account.
func TestForwardCodexResponsesViaCC_401DisablesAccountAndFailsOver(t *testing.T) {
	repo := &rateLimitAccountRepoStub{}
	rl := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusUnauthorized,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"invalid api key"}}`)),
	}}
	svc := &GatewayService{httpUpstream: upstream, rateLimitService: rl}
	body := []byte(`{"model":"kimi","stream":false,"input":"hi"}`)
	c, _ := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanKimiAccount(), body, nil)

	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Equal(t, http.StatusUnauthorized, failoverErr.StatusCode)
	require.Equal(t, 1, repo.setErrorCalls, "401 should disable the account")
}
