package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type CodexChatReasoningConfig struct {
	SupportsThinking *bool  `json:"supports_thinking,omitempty"`
	SupportsEffort   *bool  `json:"supports_effort,omitempty"`
	ThinkingParam    string `json:"thinking_param,omitempty"`
	EffortParam      string `json:"effort_param,omitempty"`
	EffortValueMode  string `json:"effort_value_mode,omitempty"`
	OutputFormat     string `json:"output_format,omitempty"`
}

type CodexChatRouteOptions struct {
	ReasoningConfig CodexChatReasoningConfig
}

type CodexToolContext struct {
	Tools []ResponsesTool
}

func CodexResponsesToChatCompletions(req ResponsesRequest, opts CodexChatRouteOptions) (ChatCompletionsRequest, CodexToolContext, error) {
	out := ChatCompletionsRequest{
		Model:       req.Model,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      req.Stream,
	}

	if req.MaxOutputTokens != nil {
		out.MaxTokens = req.MaxOutputTokens
		out.MaxCompletionTokens = req.MaxOutputTokens
	}

	var msgs []ChatMessage
	var systemContents []string

	if req.Instructions != "" {
		systemContents = append(systemContents, req.Instructions)
	}

	var inputItems []ResponsesInputItem
	if len(req.Input) > 0 {
		var s string
		if err := json.Unmarshal(req.Input, &s); err == nil {
			systemContentsMap := map[string]bool{}
			for _, sc := range systemContents {
				systemContentsMap[sc] = true
			}
			b, _ := json.Marshal(s)
			msgs = append(msgs, ChatMessage{
				Role:    "user",
				Content: b,
			})
		} else {
			_ = json.Unmarshal(req.Input, &inputItems)
		}
	}

	for _, item := range inputItems {
		switch item.Type {
		case "":
			if item.Role == "system" {
				var contentStr string
				_ = json.Unmarshal(item.Content, &contentStr)
				if contentStr != "" {
					systemContents = append(systemContents, contentStr)
				}
				continue
			}

			msg := ChatMessage{Role: item.Role}
			var contentStr string
			if err := json.Unmarshal(item.Content, &contentStr); err == nil {
				msg.Content, _ = json.Marshal(contentStr)
			} else {
				var parts []ResponsesContentPart
				if err := json.Unmarshal(item.Content, &parts); err == nil {
					var textParts []string
					for _, p := range parts {
						if p.Type == "input_text" || p.Type == "output_text" {
							textParts = append(textParts, p.Text)
						}
					}
					msg.Content, _ = json.Marshal(strings.Join(textParts, "\n"))
				} else {
					msg.Content = item.Content
				}
			}
			msgs = append(msgs, msg)

		case "function_call", "custom_tool_call":
			var lastMsg *ChatMessage
			if len(msgs) > 0 && msgs[len(msgs)-1].Role == "assistant" {
				lastMsg = &msgs[len(msgs)-1]
			} else {
				msgs = append(msgs, ChatMessage{Role: "assistant"})
				lastMsg = &msgs[len(msgs)-1]
			}
			args := item.Arguments
			if args == "" {
				args = "{}"
			}
			lastMsg.ToolCalls = append(lastMsg.ToolCalls, ChatToolCall{
				ID:   item.CallID,
				Type: "function",
				Function: ChatFunctionCall{
					Name:      strings.ReplaceAll(item.Name, "/", "__"),
					Arguments: args,
				},
			})

		case "function_call_output", "custom_tool_call_output":
			msgs = append(msgs, ChatMessage{
				Role:       "tool",
				ToolCallID: item.CallID,
				Content:    json.RawMessage(fmt.Sprintf("%q", item.Output)),
			})
		}
	}

	if len(systemContents) > 0 {
		combinedSystem := strings.Join(systemContents, "\n\n")
		b, _ := json.Marshal(combinedSystem)
		var newMsgs []ChatMessage
		newMsgs = append(newMsgs, ChatMessage{Role: "system", Content: b})
		for _, m := range msgs {
			if m.Role != "system" {
				newMsgs = append(newMsgs, m)
			}
		}
		msgs = newMsgs
	}

	out.Messages = msgs

	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			name := t.Name
			if name == "" {
				name = t.Type
			}
			name = strings.ReplaceAll(name, "/", "__")

			params := t.Parameters
			if params == nil || string(params) == "" || string(params) == "{}" {
				params = json.RawMessage(`{"type":"object","properties":{"input":{"type":"string"}}}`)
			}
			out.Tools = append(out.Tools, ChatTool{
				Type: "function",
				Function: &ChatFunction{
					Name:        name,
					Description: t.Description,
					Parameters:  params,
					Strict:      t.Strict,
				},
			})
		}
	}

	if req.Stream {
		out.StreamOptions = &ChatStreamOptions{IncludeUsage: true}
	}

	if req.Reasoning != nil && opts.ReasoningConfig.SupportsEffort != nil && *opts.ReasoningConfig.SupportsEffort {
		effort := req.Reasoning.Effort
		if opts.ReasoningConfig.EffortValueMode == "deepseek" {
			if effort == "xhigh" {
				effort = "max"
			} else if effort != "max" {
				effort = "high"
			}
		} else if opts.ReasoningConfig.EffortValueMode == "openrouter" {
			if effort == "max" {
				effort = "xhigh"
			}
		}
		out.ReasoningEffort = effort
	}

	return out, CodexToolContext{Tools: req.Tools}, nil
}

// ChatCompletionsToCodexResponses converts Chat Completions non-streaming response back to Responses API format
func ChatCompletionsToCodexResponses(chatResp ChatCompletionsResponse, ctx CodexToolContext) (ResponsesResponse, error) {
	out := ResponsesResponse{
		ID:     chatResp.ID,
		Object: "response",
		Model:  chatResp.Model,
		Status: "completed",
	}

	if len(chatResp.Choices) > 0 {
		choice := chatResp.Choices[0]
		
		switch choice.FinishReason {
		case "length":
			out.Status = "incomplete"
			out.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
		case "content_filter":
			out.Status = "incomplete"
			out.IncompleteDetails = &ResponsesIncompleteDetails{Reason: "content_filter"}
		default:
			out.Status = "completed"
		}

		var outputs []ResponsesOutput
		
		if choice.Message.ReasoningContent != "" {
			outputs = append(outputs, ResponsesOutput{
				Type: "reasoning",
				Summary: []ResponsesSummary{
					{Type: "summary_text", Text: choice.Message.ReasoningContent},
				},
			})
		}
		
		if len(choice.Message.Content) > 0 {
			var contentStr string
			_ = json.Unmarshal(choice.Message.Content, &contentStr)
			if contentStr != "" {
				outputs = append(outputs, ResponsesOutput{
					Type: "message",
					Role: "assistant",
					Content: []ResponsesContentPart{
						{Type: "output_text", Text: contentStr},
					},
				})
			}
		}
		
		for _, tc := range choice.Message.ToolCalls {
			name := strings.ReplaceAll(tc.Function.Name, "__", "/")
			outputs = append(outputs, ResponsesOutput{
				Type: "function_call",
				CallID: tc.ID,
				Name: name,
				Arguments: tc.Function.Arguments,
			})
		}
		
		out.Output = outputs
	}

	if chatResp.Usage != nil {
		out.Usage = &ResponsesUsage{
			InputTokens: chatResp.Usage.PromptTokens,
			OutputTokens: chatResp.Usage.CompletionTokens,
			TotalTokens: chatResp.Usage.TotalTokens,
		}
		if chatResp.Usage.PromptTokensDetails != nil {
			out.Usage.InputTokensDetails = &ResponsesInputTokensDetails{
				CachedTokens: chatResp.Usage.PromptTokensDetails.CachedTokens,
				AudioTokens: chatResp.Usage.PromptTokensDetails.AudioTokens,
			}
		}
		if chatResp.Usage.CompletionTokensDetails != nil {
			out.Usage.OutputTokensDetails = &ResponsesOutputTokensDetails{
				ReasoningTokens: chatResp.Usage.CompletionTokensDetails.ReasoningTokens,
				AudioTokens: chatResp.Usage.CompletionTokensDetails.AudioTokens,
				AcceptedPredictionTokens: chatResp.Usage.CompletionTokensDetails.AcceptedPredictionTokens,
				RejectedPredictionTokens: chatResp.Usage.CompletionTokensDetails.RejectedPredictionTokens,
			}
		}
	}

	return out, nil
}

// ChatEventToCodexResponsesState tracks SSE state
type ChatEventToCodexResponsesState struct {
	ID           string
	Model        string
	Created      bool
	OutputIndex  int
}

// ChatCompletionsEventToCodexResponses converts streaming chat chunks to responses chunks
func ChatCompletionsEventToCodexResponses(chunk ChatCompletionsChunk, state *ChatEventToCodexResponsesState) []ResponsesStreamEvent {
	var events []ResponsesStreamEvent

	if !state.Created {
		state.Created = true
		state.ID = chunk.ID
		if state.ID == "" {
			state.ID = "resp_" + fmt.Sprint(time.Now().UnixNano())
		}
		state.Model = chunk.Model
		
		events = append(events, ResponsesStreamEvent{
			Type: "response.created",
			Response: &ResponsesResponse{
				ID: state.ID,
				Object: "response",
				Model: state.Model,
			},
		})
	}

	for _, choice := range chunk.Choices {
		if choice.Delta.ReasoningContent != nil && *choice.Delta.ReasoningContent != "" {
			events = append(events, ResponsesStreamEvent{
				Type: "response.reasoning_summary_text.delta",
				OutputIndex: state.OutputIndex,
				Delta: *choice.Delta.ReasoningContent,
				Text: *choice.Delta.ReasoningContent,
			})
		}
		
		if choice.Delta.Content != nil && *choice.Delta.Content != "" {
			events = append(events, ResponsesStreamEvent{
				Type: "response.output_text.delta",
				OutputIndex: state.OutputIndex,
				Delta: *choice.Delta.Content,
				Text: *choice.Delta.Content,
			})
		}
		
		for _, tc := range choice.Delta.ToolCalls {
			if tc.Function.Name != "" {
				name := strings.ReplaceAll(tc.Function.Name, "__", "/")
				events = append(events, ResponsesStreamEvent{
					Type: "response.output_item.added",
					OutputIndex: state.OutputIndex,
					Item: &ResponsesOutput{
						Type: "function_call",
						CallID: tc.ID,
						Name: name,
					},
				})
			}
			if tc.Function.Arguments != "" {
				events = append(events, ResponsesStreamEvent{
					Type: "response.function_call_arguments.delta",
					OutputIndex: state.OutputIndex,
					CallID: tc.ID,
					Arguments: tc.Function.Arguments,
					Delta: tc.Function.Arguments,
				})
			}
		}

		if choice.FinishReason != nil {
			status := "completed"
			var incomplete *ResponsesIncompleteDetails
			
			switch *choice.FinishReason {
			case "length":
				status = "incomplete"
				incomplete = &ResponsesIncompleteDetails{Reason: "max_output_tokens"}
			case "content_filter":
				status = "incomplete"
				incomplete = &ResponsesIncompleteDetails{Reason: "content_filter"}
			case "tool_calls":
				events = append(events, ResponsesStreamEvent{
					Type: "response.output_item.done",
					OutputIndex: state.OutputIndex,
				})
				state.OutputIndex++
			}
			
			events = append(events, ResponsesStreamEvent{
				Type: "response.done", 
				Response: &ResponsesResponse{
					ID: state.ID,
					Object: "response",
					Model: state.Model,
					Status: status,
					IncompleteDetails: incomplete,
				},
			})
		}
	}

	if chunk.Usage != nil {
		usage := &ResponsesUsage{
			InputTokens: chunk.Usage.PromptTokens,
			OutputTokens: chunk.Usage.CompletionTokens,
			TotalTokens: chunk.Usage.TotalTokens,
		}
		if chunk.Usage.PromptTokensDetails != nil {
			usage.InputTokensDetails = &ResponsesInputTokensDetails{
				CachedTokens: chunk.Usage.PromptTokensDetails.CachedTokens,
				AudioTokens: chunk.Usage.PromptTokensDetails.AudioTokens,
			}
		}
		if chunk.Usage.CompletionTokensDetails != nil {
			usage.OutputTokensDetails = &ResponsesOutputTokensDetails{
				ReasoningTokens: chunk.Usage.CompletionTokensDetails.ReasoningTokens,
				AudioTokens: chunk.Usage.CompletionTokensDetails.AudioTokens,
				AcceptedPredictionTokens: chunk.Usage.CompletionTokensDetails.AcceptedPredictionTokens,
				RejectedPredictionTokens: chunk.Usage.CompletionTokensDetails.RejectedPredictionTokens,
			}
		}
		events = append(events, ResponsesStreamEvent{
			Type: "response.done",
			Response: &ResponsesResponse{
				ID: state.ID,
				Object: "response",
				Model: state.Model,
				Status: "completed",
				Usage: usage,
			},
			Usage: usage,
		})
	}

	return events
}
