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

func newCodingPlanThinkAccount() *Account {
	return &Account{
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
}

// collectResponsesOutputTextDeltas concatenates the `delta` of every
// response.output_text.delta SSE event in a streamed Responses body.
func collectResponsesOutputTextDeltas(t *testing.T, body string) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimSpace(line[6:])
		if payload == "" || payload == "[DONE]" {
			continue
		}
		if gjson.Get(payload, "type").String() == "response.output_text.delta" {
			b.WriteString(gjson.Get(payload, "delta").String())
		}
	}
	return b.String()
}

// TestForwardCodexResponsesViaChatCompletions_BufferedStripsThinkTag verifies that
// `<think>...</think>` embedded in assistant content is removed from the buffered
// Responses output_text (task C4.1).
func TestForwardCodexResponsesViaChatCompletions_BufferedStripsThinkTag(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"<think>secret</think>你好"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n"))}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi","stream":false,"input":"hi"}`)
	c, rec := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanThinkAccount(), body, nil)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, http.StatusOK, rec.Code)

	outText := gjson.GetBytes(rec.Body.Bytes(), `output.#(type=="message").content.0.text`).String()
	require.Equal(t, "你好", outText)
	require.NotContains(t, outText, "secret")
	require.NotContains(t, outText, "<think>")
	// The reasoning is preserved as a reasoning summary, not in output_text.
	require.Equal(t, "secret", gjson.GetBytes(rec.Body.Bytes(), `output.#(type=="reasoning").summary.0.text`).String())
}

// TestForwardCodexResponsesViaChatCompletions_StreamingStripsThinkTagAcrossChunks
// verifies that a `<think>` block split across SSE chunks is removed from the
// streamed output_text deltas (task C4.2).
func TestForwardCodexResponsesViaChatCompletions_StreamingStripsThinkTagAcrossChunks(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"<thi"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"nk>secret</think>"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"你好"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n"))}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi","stream":true,"input":"hi"}`)
	c, rec := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanThinkAccount(), body, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	deltas := collectResponsesOutputTextDeltas(t, rec.Body.String())
	require.Equal(t, "你好", deltas)
	// In streaming, think reasoning is dropped entirely — it must not appear in
	// any event of the stream.
	require.NotContains(t, rec.Body.String(), "secret")
}

// TestForwardCodexResponsesViaChatCompletions_BufferedNativeReasoningNotInOutputText
// verifies that a structured reasoning_content delta never leaks into the buffered
// output_text (task C4.3).
func TestForwardCodexResponsesViaChatCompletions_BufferedNativeReasoningNotInOutputText(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"reasoning_content":"native-reasoning"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"final answer"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n"))}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi","stream":false,"input":"hi"}`)
	c, rec := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanThinkAccount(), body, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	outText := gjson.GetBytes(rec.Body.Bytes(), `output.#(type=="message").content.0.text`).String()
	require.Equal(t, "final answer", outText)
	require.NotContains(t, outText, "native-reasoning")
}

// TestForwardCodexResponsesViaChatCompletions_BufferedUnclosedThinkNoLeak verifies
// that an unclosed `<think>` block keeps its body out of output_text (task C4.4).
func TestForwardCodexResponsesViaChatCompletions_BufferedUnclosedThinkNoLeak(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"visible answer <think>secret never closed"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n"))}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi","stream":false,"input":"hi"}`)
	c, rec := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanThinkAccount(), body, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	outText := gjson.GetBytes(rec.Body.Bytes(), `output.#(type=="message").content.0.text`).String()
	require.Equal(t, "visible answer ", outText)
	require.NotContains(t, outText, "secret")
}

// TestForwardCodexResponsesViaChatCompletions_BufferedMultipleThinkBlocks verifies
// that multiple think blocks are each stripped (task C4.5).
func TestForwardCodexResponsesViaChatCompletions_BufferedMultipleThinkBlocks(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"<think>a</think>one<think>b</think>two"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n"))}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi","stream":false,"input":"hi"}`)
	c, rec := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanThinkAccount(), body, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	outText := gjson.GetBytes(rec.Body.Bytes(), `output.#(type=="message").content.0.text`).String()
	require.Equal(t, "onetwo", outText)
}

// TestForwardCodexResponsesViaChatCompletions_BufferedKeepsThinkingTag verifies
// that a `<thinking>` lookalike is NOT stripped (task C4.6).
func TestForwardCodexResponsesViaChatCompletions_BufferedKeepsThinkingTag(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: newCodingPlanSSE(http.StatusOK, strings.Join([]string{
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{"content":"<thinking>kept</thinking> done"}}]}`,
		`data: {"id":"c1","object":"chat.completion.chunk","created":1,"model":"kimi","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
		`data: [DONE]`,
		``,
	}, "\n"))}
	svc := &GatewayService{httpUpstream: upstream}
	body := []byte(`{"model":"kimi","stream":false,"input":"hi"}`)
	c, rec := newCodingPlanResponsesTestContext(body)

	result, err := svc.ForwardCodexResponsesViaChatCompletions(context.Background(), c, newCodingPlanThinkAccount(), body, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	outText := gjson.GetBytes(rec.Body.Bytes(), `output.#(type=="message").content.0.text`).String()
	require.Equal(t, "<thinking>kept</thinking> done", outText)
}
