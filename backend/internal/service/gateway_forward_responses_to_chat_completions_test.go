package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newCodingPlanSSE(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewBufferString(body)),
	}
}

func newCodingPlanResponsesTestContext(body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, rec
}

func TestIsEmptyResponsesInput(t *testing.T) {
	tests := []struct {
		name string
		raw  []byte
		want bool
	}{
		{name: "missing", raw: nil, want: true},
		{name: "null", raw: []byte("null"), want: true},
		{name: "empty string", raw: []byte(`""`), want: true},
		{name: "empty array", raw: []byte(`[]`), want: true},
		{name: "text", raw: []byte(`"hello"`), want: false},
		{name: "items", raw: []byte(`[{"role":"user","content":"hello"}]`), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, isEmptyResponsesInput(tt.raw))
		})
	}
}

func TestForwardCodexResponsesViaChatCompletions_DropsPreviousResponseIDWithFullInput(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, strings.Join([]string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"kimi-k2-turbo-preview","choices":[{"index":0,"delta":{"content":"ok"}}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","created":1,"model":"kimi-k2-turbo-preview","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n"))}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi-k2-turbo-preview","stream":false,"previous_response_id":"resp_123","input":"hello"}`)
	c, rec := newCodingPlanResponsesTestContext(body)
	account := &Account{
		ID:       1,
		Platform: "kimi",
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-kimi",
			"base_url": "https://api.moonshot.cn/v1",
		},
		Extra: map[string]any{
			"coding_plan_provider": "kimi",
		},
	}

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, account, body, nil)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "https://api.moonshot.cn/v1/chat/completions", upstream.lastReq.URL.String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "previous_response_id").Exists())
	require.Equal(t, "kimi-k2-turbo-preview", gjson.GetBytes(upstream.lastBody, "model").String())
	require.Equal(t, "hello", gjson.GetBytes(upstream.lastBody, "messages.0.content").String())
}

func TestForwardCodexResponsesViaChatCompletions_RejectsPreviousResponseIDWithoutInput(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, "")}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi-k2-turbo-preview","stream":false,"previous_response_id":"resp_123"}`)
	c, rec := newCodingPlanResponsesTestContext(body)
	account := &Account{
		ID:       1,
		Platform: "kimi",
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-kimi",
			"base_url": "https://api.moonshot.cn/v1",
		},
		Extra: map[string]any{
			"coding_plan_provider": "kimi",
		},
	}

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, account, body, nil)

	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Contains(t, rec.Body.String(), "conversation state cannot be restored")
	require.Nil(t, upstream.lastReq)
}
