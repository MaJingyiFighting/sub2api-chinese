package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

type routerConfig struct {
	ListenAddr   string
	UpstreamURL  string
	APIKey       string
	ModelMapping map[string]string
}

func main() {
	cfg := loadConfig()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		handleResponses(w, r, cfg)
	})
	mux.HandleFunc("/responses", func(w http.ResponseWriter, r *http.Request) {
		handleResponses(w, r, cfg)
	})
	log.Printf("codex-router listening on %s; upstream=%s", cfg.ListenAddr, cfg.UpstreamURL)
	if err := http.ListenAndServe(cfg.ListenAddr, mux); err != nil {
		log.Fatal(err)
	}
}

func loadConfig() routerConfig {
	upstreamBase := strings.TrimRight(firstNonEmpty(os.Getenv("CODEX_ROUTER_UPSTREAM_BASE_URL"), os.Getenv("UPSTREAM_BASE_URL")), "/")
	if upstreamBase == "" {
		upstreamBase = "http://127.0.0.1:8000/v1"
	}
	cfg := routerConfig{
		ListenAddr:  firstNonEmpty(os.Getenv("CODEX_ROUTER_LISTEN"), ":8089"),
		UpstreamURL: upstreamBase + "/chat/completions",
		APIKey:      firstNonEmpty(os.Getenv("CODEX_ROUTER_API_KEY"), os.Getenv("UPSTREAM_API_KEY")),
	}
	if raw := strings.TrimSpace(os.Getenv("CODEX_ROUTER_MODEL_MAPPING")); raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg.ModelMapping)
	}
	return cfg
}

func handleResponses(w http.ResponseWriter, r *http.Request, cfg routerConfig) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "failed to read request body")
		return
	}
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "failed to parse request body")
		return
	}
	if responsesReq.PreviousResponseID != "" && isEmptyResponsesInput(responsesReq.Input) {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", "previous_response_id is not supported by this stateless converter without full input/history")
		return
	}
	responsesReq.PreviousResponseID = ""
	originalModel := responsesReq.Model
	if mapped := cfg.ModelMapping[responsesReq.Model]; mapped != "" {
		responsesReq.Model = mapped
	}
	chatReq, _, err := apicompat.CodexResponsesToChatCompletions(responsesReq, apicompat.CodexChatRouteOptions{})
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	upstreamResp, err := postChatCompletions(r, cfg, chatReq)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "server_error", err.Error())
		return
	}
	defer upstreamResp.Body.Close()
	if upstreamResp.StatusCode >= 400 {
		w.WriteHeader(upstreamResp.StatusCode)
		_, _ = io.Copy(w, upstreamResp.Body)
		return
	}
	if responsesReq.Stream {
		streamResponses(w, upstreamResp.Body, originalModel)
		return
	}
	bufferedResponses(w, upstreamResp.Body, originalModel)
}

func postChatCompletions(r *http.Request, cfg routerConfig, chatReq apicompat.ChatCompletionsRequest) (*http.Response, error) {
	body, err := json.Marshal(chatReq)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, cfg.UpstreamURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if chatReq.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	if cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	}
	return http.DefaultClient.Do(req)
}

func bufferedResponses(w http.ResponseWriter, body io.Reader, originalModel string) {
	var chatResp apicompat.ChatCompletionsResponse
	if err := json.NewDecoder(body).Decode(&chatResp); err != nil {
		writeJSONError(w, http.StatusBadGateway, "server_error", "failed to decode upstream response")
		return
	}
	cleanBufferedThinkTags(&chatResp)
	resp, err := apicompat.ChatCompletionsToCodexResponses(chatResp, apicompat.CodexToolContext{})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, "server_error", err.Error())
		return
	}
	if originalModel != "" {
		resp.Model = originalModel
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func streamResponses(w http.ResponseWriter, body io.Reader, originalModel string) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	state := &apicompat.ChatEventToCodexResponsesState{Model: originalModel}
	extractor := &apicompat.ThinkTagExtractor{}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") {
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			continue
		}
		var chunk apicompat.ChatCompletionsChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		cleanStreamingThinkTags(&chunk, extractor)
		if originalModel != "" {
			chunk.Model = originalModel
		}
		for _, event := range apicompat.ChatCompletionsEventToCodexResponses(chunk, state) {
			writeSSE(w, event)
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		writeSSE(w, map[string]any{"type": "response.error", "error": err.Error()})
	}
}

func cleanBufferedThinkTags(resp *apicompat.ChatCompletionsResponse) {
	for i := range resp.Choices {
		var content string
		if len(resp.Choices[i].Message.Content) == 0 {
			continue
		}
		if err := json.Unmarshal(resp.Choices[i].Message.Content, &content); err != nil {
			continue
		}
		extractor := &apicompat.ThinkTagExtractor{}
		visible, reasoning := extractor.Push(content)
		flushVisible, flushReasoning := extractor.Flush()
		visible += flushVisible
		reasoning += flushReasoning
		resp.Choices[i].Message.Content, _ = json.Marshal(visible)
		if reasoning != "" {
			if resp.Choices[i].Message.ReasoningContent != "" {
				resp.Choices[i].Message.ReasoningContent += "\n"
			}
			resp.Choices[i].Message.ReasoningContent += reasoning
		}
	}
}

func cleanStreamingThinkTags(chunk *apicompat.ChatCompletionsChunk, extractor *apicompat.ThinkTagExtractor) {
	for i := range chunk.Choices {
		content := chunk.Choices[i].Delta.Content
		if content == nil || *content == "" {
			continue
		}
		visible, reasoning := extractor.Push(*content)
		chunk.Choices[i].Delta.Content = &visible
		if reasoning != "" {
			chunk.Choices[i].Delta.ReasoningContent = &reasoning
		}
	}
}

func writeSSE(w http.ResponseWriter, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}

func writeJSONError(w http.ResponseWriter, status int, code string, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"type":    code,
			"code":    code,
			"message": message,
		},
	})
}

func isEmptyResponsesInput(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte(`""`)) || bytes.Equal(trimmed, []byte("[]"))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
