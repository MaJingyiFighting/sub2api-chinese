package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandleResponsesForwardsSub2APIKeyAndMappedModel(t *testing.T) {
	var gotAuthorization string
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		gotModel, _ = body["model"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-test",
			"object":"chat.completion",
			"created":1,
			"model":"MiniMax-M3",
			"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer upstream.Close()

	cfg := routerConfig{
		UpstreamURL: upstream.URL,
		ModelMapping: map[string]string{
			"gpt-5-codex": "MiniMax-M3",
		},
		Client: upstream.Client(),
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"gpt-5-codex",
		"input":"hello",
		"stream":false
	}`))
	req.Header.Set("Authorization", "Bearer sub2api-key")
	recorder := httptest.NewRecorder()

	handleResponses(recorder, req, cfg)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "Bearer sub2api-key", gotAuthorization)
	require.Equal(t, "MiniMax-M3", gotModel)
	require.Contains(t, recorder.Body.String(), `"model":"gpt-5-codex"`)
	require.Contains(t, recorder.Body.String(), `"output"`)
}

func TestHandleResponsesRejectsPreviousResponseWithoutHistory(t *testing.T) {
	cfg := routerConfig{Client: http.DefaultClient}
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{
		"model":"gpt-5-codex",
		"previous_response_id":"resp_previous"
	}`))
	recorder := httptest.NewRecorder()

	handleResponses(recorder, req, cfg)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Contains(t, recorder.Body.String(), "full input/history")
}
