package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// ForwardCodexResponsesViaChatCompletions accepts an OpenAI Responses API request body,
// converts it to Chat Completions format, forwards to a Chat Completions upstream
// (such as domestic Coding Plan endpoints), and converts the response back to Responses format.
func (s *GatewayService) ForwardCodexResponsesViaChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *ParsedRequest,
) (*ForwardResult, error) {
	startTime := time.Now()

	// 1. Parse Responses request
	var responsesReq apicompat.ResponsesRequest
	if err := json.Unmarshal(body, &responsesReq); err != nil {
		return nil, fmt.Errorf("parse responses request: %w", err)
	}
	originalModel := responsesReq.Model
	clientStream := responsesReq.Stream

	// 2. Handle `previous_response_id`:
	//    国产 Coding Plan 的 Chat Completions 上游不支持 Codex Responses 的服务端
	//    会话状态恢复，无法仅凭 previous_response_id 重建历史。
	//    但 Codex CLI 在大多数情况下会同时把完整历史塞进 `input`，仅把
	//    previous_response_id 作为附加引用；这种请求实际可以无损降级。
	//
	//    策略：
	//      - 若客户端同时提供了非空 input → 视为完整上下文请求，丢弃
	//        previous_response_id 并记录警告，让请求继续（避免 Codex CLI 整体阻断）。
	//      - 若 input 为空（客户端真依赖上游会话状态）→ 报错并提示客户端
	//        重发完整上下文。
	if responsesReq.PreviousResponseID != "" {
		hasInput := !isEmptyResponsesInput(responsesReq.Input)
		if !hasInput {
			writeResponsesError(c, http.StatusBadRequest, "invalid_request_error",
				"previous_response_id is not supported on this upstream because conversation state cannot be restored from id alone; please resend the full input/history (this provider exposes only Chat Completions, which is stateless).")
			return nil, fmt.Errorf("previous_response_id without full input not supported")
		}
		logger.L().Warn("gateway forward_responses_via_cc: dropping previous_response_id (stateless upstream)",
			zap.Int64("account_id", account.ID),
			zap.String("platform", account.Platform),
			zap.Int("input_len", len(responsesReq.Input)),
		)
		responsesReq.PreviousResponseID = ""
	}

	// 3. Convert Responses → Chat Completions
	ccReq, err := apicompat.ResponsesToChatCompletionsRequest(&responsesReq)
	if err != nil {
		return nil, fmt.Errorf("convert responses to chat completions: %w", err)
	}

	// 4. Force upstream streaming
	ccReq.Stream = true
	reqStream := true

	// 5. Model 已由 handler 通过 ReplaceModelInBody 写入最终的上游真实模型名，
	//    此处直接沿用，避免对同一映射表二次查找（mapping 含链式键时可能再被替换一次）。
	mappedModel := originalModel
	reasoningEffort := ExtractResponsesReasoningEffortFromBody(body)
	ccReq.Model = mappedModel

	logger.L().Debug("gateway forward_responses_via_cc: model mapping applied",
		zap.Int64("account_id", account.ID),
		zap.String("original_model", originalModel),
		zap.String("mapped_model", mappedModel),
		zap.Bool("client_stream", clientStream),
	)

	// 6. Marshal Chat Completions request body
	ccBody, err := json.Marshal(ccReq)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions request: %w", err)
	}

	// 7. Get access token
	token := accountCodingPlanAPIKey(account)

	// 8. Build Target URL
	baseURL := accountCodingPlanBaseURL(account)
	if baseURL == "" {
		return nil, fmt.Errorf("no base url configured for coding plan account")
	}
	targetURL := strings.TrimRight(baseURL, "/") + "/chat/completions"

	// 9. Build upstream request
	upstreamCtx, releaseUpstreamCtx := detachStreamUpstreamContext(ctx, reqStream)
	defer releaseUpstreamCtx()

	req, err := http.NewRequestWithContext(upstreamCtx, http.MethodPost, targetURL, bytes.NewReader(ccBody))
	if err != nil {
		return nil, fmt.Errorf("build upstream request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// 10. Send request
	proxyURL := ""
	if account.ProxyID != nil && account.Proxy != nil {
		proxyURL = account.Proxy.URL()
	}

	resp, err := s.httpUpstream.DoWithTLS(req, proxyURL, account.ID, account.Concurrency, s.tlsFPProfileService.ResolveTLSProfile(account))
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		safeErr := sanitizeUpstreamErrorMessage(err.Error())
		setOpsUpstreamError(c, 0, safeErr, "")
		appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
			Platform:           account.Platform,
			AccountID:          account.ID,
			AccountName:        account.Name,
			UpstreamStatusCode: 0,
			Kind:               "request_error",
			Message:            safeErr,
		})
		writeResponsesError(c, http.StatusBadGateway, "server_error", "Upstream request failed")
		return nil, fmt.Errorf("upstream request failed: %s", safeErr)
	}
	defer func() { _ = resp.Body.Close() }()

	// 11. Handle error response with failover
	if resp.StatusCode >= 400 {
		respBody, _ := s.readUpstreamErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		upstreamMsg := strings.TrimSpace(extractUpstreamErrorMessage(respBody))
		upstreamMsg = sanitizeUpstreamErrorMessage(upstreamMsg)

		if s.shouldFailoverUpstreamError(resp.StatusCode) {
			appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
				Platform:           account.Platform,
				AccountID:          account.ID,
				AccountName:        account.Name,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.Header.Get("x-request-id"),
				Kind:               "failover",
				Message:            upstreamMsg,
			})
			if s.rateLimitService != nil {
				s.rateLimitService.HandleUpstreamError(ctx, account, resp.StatusCode, resp.Header, respBody, mappedModel)
			}
			return nil, &UpstreamFailoverError{
				StatusCode:   resp.StatusCode,
				ResponseBody: respBody,
			}
		}

		writeResponsesError(c, mapUpstreamStatusCode(resp.StatusCode), "server_error", upstreamMsg)
		return nil, fmt.Errorf("upstream error: %d %s", resp.StatusCode, upstreamMsg)
	}

	// 12. Handle normal response
	var result *ForwardResult
	var handleErr error
	if clientStream {
		result, handleErr = s.handleResponsesStreamingFromCC(resp, c, originalModel, mappedModel, reasoningEffort, startTime)
	} else {
		result, handleErr = s.handleResponsesBufferedFromCC(resp, c, originalModel, mappedModel, reasoningEffort, startTime)
	}

	return result, handleErr
}

// isEmptyResponsesInput reports whether a Responses API `input` field carries no
// usable content. This is used to decide if a `previous_response_id`-only request
// (no full history) should be rejected, since stateless upstreams can't recover
// conversation state from the id alone.
//
// `input` may be:
//   - missing (raw is nil/zero-len after JSON unmarshal)
//   - JSON null
//   - empty string ""
//   - empty array []
//
// Anything else (a non-empty string or a non-empty array of items) counts as
// substantive history and lets the request proceed.
func isEmptyResponsesInput(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return true
	}
	if bytes.Equal(trimmed, []byte("null")) {
		return true
	}
	if bytes.Equal(trimmed, []byte(`""`)) {
		return true
	}
	if bytes.Equal(trimmed, []byte("[]")) {
		return true
	}
	return false
}

// handleResponsesBufferedFromCC reads Chat Completions SSE events, assembles the full
// response, then converts Chat Completions → Responses.
func (s *GatewayService) handleResponsesBufferedFromCC(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
) (*ForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	var usage ClaudeUsage

	ccResp := &apicompat.ChatCompletionsResponse{
		ID:      "", // ChatCompletionsResponseToResponses will generate a new Responses ID
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   mappedModel,
		Choices: []apicompat.ChatChoice{{
			Index: 0,
			Message: apicompat.ChatMessage{
				Role: "assistant",
			},
			FinishReason: "stop",
		}},
	}

	var contentBuilder strings.Builder
	var reasoningBuilder strings.Builder

	// Some Coding Plan upstreams embed chain-of-thought in the assistant content
	// as `<think>...</think>` instead of a structured reasoning_content field.
	// Strip those blocks (across chunk boundaries) so they never reach the
	// user-visible Responses output_text; the extracted reasoning is preserved in
	// reasoningBuilder.
	thinkExtractor := &apicompat.ThinkTagExtractor{}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if strings.TrimSpace(payload) == "[DONE]" {
			break
		}

		var chunk apicompat.ChatCompletionsChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
			ccResp.Usage = chunk.Usage
		}

		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta
			if delta.Content != nil {
				visible, reasoning := thinkExtractor.Push(*delta.Content)
				contentBuilder.WriteString(visible)
				reasoningBuilder.WriteString(reasoning)
			}
			if delta.ReasoningContent != nil {
				reasoningBuilder.WriteString(*delta.ReasoningContent)
			}
			if chunk.Choices[0].FinishReason != nil {
				ccResp.Choices[0].FinishReason = *chunk.Choices[0].FinishReason
			}
			
			for _, tc := range delta.ToolCalls {
				idx := 0
				if tc.Index != nil {
					idx = *tc.Index
				}
				for len(ccResp.Choices[0].Message.ToolCalls) <= idx {
					ccResp.Choices[0].Message.ToolCalls = append(ccResp.Choices[0].Message.ToolCalls, apicompat.ChatToolCall{
						Type: "function",
					})
				}
				if tc.ID != "" {
					ccResp.Choices[0].Message.ToolCalls[idx].ID = tc.ID
				}
				if tc.Function.Name != "" {
					ccResp.Choices[0].Message.ToolCalls[idx].Function.Name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					ccResp.Choices[0].Message.ToolCalls[idx].Function.Arguments += tc.Function.Arguments
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("forward_responses_via_cc buffered: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}

	// Drain any buffered partial `<think>` fragment left at end-of-stream.
	if visible, reasoning := thinkExtractor.Flush(); visible != "" || reasoning != "" {
		contentBuilder.WriteString(visible)
		reasoningBuilder.WriteString(reasoning)
	}

	if contentBuilder.Len() > 0 {
		ccBody, _ := json.Marshal(contentBuilder.String())
		ccResp.Choices[0].Message.Content = ccBody
	}
	if reasoningBuilder.Len() > 0 {
		ccResp.Choices[0].Message.ReasoningContent = reasoningBuilder.String()
	}

	responsesResp := apicompat.ChatCompletionsResponseToResponses(ccResp, originalModel)

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	if respBytes, err := json.Marshal(responsesResp); err == nil {
		respBytes = reverseToolNamesIfPresent(c, respBytes)
		c.Data(http.StatusOK, "application/json; charset=utf-8", respBytes)
	} else {
		c.JSON(http.StatusOK, responsesResp)
	}

	return &ForwardResult{
		RequestID:       requestID,
		Usage:           usage,
		Model:           originalModel,
		UpstreamModel:   mappedModel,
		ReasoningEffort: reasoningEffort,
		Stream:          false,
		Duration:        time.Since(startTime),
	}, nil
}

// handleResponsesStreamingFromCC reads Chat Completions SSE chunks, converts each
// to Responses events, and writes them.
func (s *GatewayService) handleResponsesStreamingFromCC(
	resp *http.Response,
	c *gin.Context,
	originalModel string,
	mappedModel string,
	reasoningEffort *string,
	startTime time.Time,
) (*ForwardResult, error) {
	requestID := resp.Header.Get("x-request-id")

	if s.responseHeaderFilter != nil {
		responseheaders.WriteFilteredHeaders(c.Writer.Header(), resp.Header, s.responseHeaderFilter)
	}
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
	c.Writer.WriteHeader(http.StatusOK)

	ccState := apicompat.NewChatCompletionsToResponsesStreamState(originalModel)

	// Strip inline `<think>...</think>` reasoning from the visible content before
	// converting to Responses events, so it never reaches output_text deltas. The
	// extractor is stateful across chunks (tags may be split on any boundary).
	thinkExtractor := &apicompat.ThinkTagExtractor{}

	var usage ClaudeUsage
	var firstTokenMs *int
	firstChunk := true

	scanner := bufio.NewScanner(resp.Body)
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	scanner.Buffer(make([]byte, 0, 64*1024), maxLineSize)

	resultWithUsage := func() *ForwardResult {
		return &ForwardResult{
			RequestID:       requestID,
			Usage:           usage,
			Model:           originalModel,
			UpstreamModel:   mappedModel,
			ReasoningEffort: reasoningEffort,
			Stream:          true,
			Duration:        time.Since(startTime),
			FirstTokenMs:    firstTokenMs,
		}
	}

	writeEvent := func(event apicompat.ResponsesStreamEvent) bool {
		sse, err := apicompat.ResponsesEventToSSE(event)
		if err != nil {
			return false
		}
		out := string(reverseToolNamesIfPresent(c, []byte(sse)))
		if _, err := fmt.Fprint(c.Writer, out); err != nil {
			return true // client disconnected
		}
		return false
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := line[6:]
		if strings.TrimSpace(payload) == "[DONE]" {
			break
		}

		if firstChunk {
			firstChunk = false
			ms := int(time.Since(startTime).Milliseconds())
			firstTokenMs = &ms
		}

		var chunk apicompat.ChatCompletionsChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			continue
		}

		if chunk.Usage != nil {
			usage.InputTokens = chunk.Usage.PromptTokens
			usage.OutputTokens = chunk.Usage.CompletionTokens
		}

		// Sanitize visible content: strip `<think>` blocks before conversion.
		// Reasoning extracted from think tags is dropped from the stream (it must
		// not become an output_text delta); native delta.reasoning_content is left
		// untouched and still surfaces as a reasoning summary.
		sanitizeChatChunkThinkTags(&chunk, thinkExtractor)

		resEvents := apicompat.ChatCompletionsChunkToResponsesEvents(&chunk, ccState)
		for _, evt := range resEvents {
			if disconnected := writeEvent(evt); disconnected {
				return resultWithUsage(), nil
			}
		}
		if len(resEvents) > 0 {
			c.Writer.Flush()
		}
	}

	if err := scanner.Err(); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			logger.L().Warn("forward_responses_via_cc stream: read error",
				zap.Error(err),
				zap.String("request_id", requestID),
			)
		}
	}

	// Emit any visible text left buffered in a partial `<think>` fragment.
	if visible, _ := thinkExtractor.Flush(); visible != "" {
		flushChunk := &apicompat.ChatCompletionsChunk{
			Choices: []apicompat.ChatChunkChoice{{Delta: apicompat.ChatDelta{Content: &visible}}},
		}
		for _, evt := range apicompat.ChatCompletionsChunkToResponsesEvents(flushChunk, ccState) {
			writeEvent(evt) //nolint:errcheck
		}
	}

	finalEvents := apicompat.FinalizeChatCompletionsResponsesStream(ccState)
	for _, evt := range finalEvents {
		writeEvent(evt) //nolint:errcheck
	}
	c.Writer.Flush()

	return resultWithUsage(), nil
}

// sanitizeChatChunkThinkTags rewrites each choice's delta.content in place so that
// `<think>...</think>` reasoning is removed from the user-visible content before
// the chunk is converted to Responses events. The extractor keeps cross-chunk
// state, so think blocks split across chunk boundaries are handled correctly.
//
// Reasoning recovered from think tags is intentionally dropped (not converted to a
// reasoning summary event) so it cannot appear anywhere in the user-visible
// output_text stream. A chunk whose visible content becomes empty after stripping
// yields an empty-string content delta, which the bridge skips — preserving any
// tool-call / finish_reason / usage carried by the same chunk.
func sanitizeChatChunkThinkTags(chunk *apicompat.ChatCompletionsChunk, extractor *apicompat.ThinkTagExtractor) {
	if chunk == nil || extractor == nil {
		return
	}
	for i := range chunk.Choices {
		content := chunk.Choices[i].Delta.Content
		if content == nil {
			continue
		}
		visible, _ := extractor.Push(*content)
		chunk.Choices[i].Delta.Content = &visible
	}
}
