package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Responses handles OpenAI Responses API endpoint for Anthropic platform groups.
// POST /v1/responses
// This converts Responses API requests to Anthropic format, forwards to Anthropic
// upstream, and converts responses back to Responses format.
func (h *GatewayHandler) CodingPlanResponses(c *gin.Context) {
	streamStarted := false

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.responsesErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.responsesErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.gateway.responses",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// Read request body
	body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.responsesErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	setOpsRequestContext(c, "", false)

	// Validate JSON
	if !gjson.ValidBytes(body) {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}

	// Extract model and stream using gjson (like OpenAI handler)
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	reqStream, ok := parseOpenAICompatibleStream(body)
	if !ok {
		h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage)
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	setOpsRequestContext(c, reqModel, reqStream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))
	requestCtx := c.Request.Context()
	if service.IsImageGenerationIntent("/v1/responses", reqModel, body) {
		requestCtx = service.WithOpenAIImageGenerationIntent(requestCtx)
	}

	// 解析渠道级模型映射
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(requestCtx, apiKey.GroupID, reqModel)

	// Claude Code only restriction:
	// /v1/responses is never a Claude Code endpoint.
	// When claude_code_only is enabled, this endpoint is rejected.
	// The existing service-layer checkClaudeCodeRestriction handles degradation
	// to fallback groups when the Forward path calls SelectAccountForModelWithExclusions.
	// Here we just reject at handler level since /v1/responses clients can't be Claude Code.
	if apiKey.Group != nil && apiKey.Group.ClaudeCodeOnly {
		h.responsesErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group is restricted to Claude Code clients (/v1/messages only)")
		return
	}

	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIResponses, reqModel, body); decision != nil && decision.Blocked {
		h.responsesErrorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}

	// Error passthrough binding
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())

	// 1. Acquire user concurrency slot
	maxWait := service.CalculateMaxWait(subject.Concurrency)
	canWait, err := h.concurrencyHelper.IncrementWaitCount(c.Request.Context(), subject.UserID, maxWait)
	waitCounted := false
	if err != nil {
		reqLog.Warn("gateway.responses.user_wait_counter_increment_failed", zap.Error(err))
	} else if !canWait {
		h.responsesErrorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many pending requests, please retry later")
		return
	}
	if err == nil && canWait {
		waitCounted = true
	}
	defer func() {
		if waitCounted {
			h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
		}
	}()

	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		reqLog.Warn("gateway.responses.user_slot_acquire_failed", zap.Error(err))
		h.handleConcurrencyError(c, err, "user", streamStarted)
		return
	}
	if waitCounted {
		h.concurrencyHelper.DecrementWaitCount(c.Request.Context(), subject.UserID)
		waitCounted = false
	}
	userReleaseFunc = wrapReleaseOnDone(c.Request.Context(), userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. Re-check billing
	if err := h.billingCacheService.CheckBillingEligibility(requestCtx, apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(requestCtx, apiKey)); err != nil {
		reqLog.Info("gateway.responses.billing_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.responsesErrorResponse(c, status, code, message)
		return
	}

	// Parse request for session hash
	bodyRef := service.NewRequestBodyRef(body)
	parsedReq, _ := service.ParseGatewayRequest(bodyRef, "responses")
	if parsedReq == nil {
		parsedReq = &service.ParsedRequest{Model: reqModel, Stream: reqStream, Body: bodyRef}
	}
	parsedReq.SessionContext = &service.SessionContext{
		ClientIP:  ip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	sessionHash := h.gatewayService.GenerateSessionHash(parsedReq)

	// 3. Account selection + failover loop
	fs := NewFailoverState(h.maxAccountSwitches, false)

	for {
		selection, err := h.gatewayService.SelectAccountWithLoadAwareness(requestCtx, apiKey.GroupID, sessionHash, reqModel, fs.FailedAccountIDs, "", int64(0))
		if err != nil {
			if len(fs.FailedAccountIDs) == 0 {
				markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				h.responsesErrorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts: "+err.Error())
				return
			}
			action := fs.HandleSelectionExhausted(requestCtx)
			switch action {
			case FailoverContinue:
				continue
			case FailoverCanceled:
				return
			default:
				if fs.LastFailoverErr != nil {
					h.handleResponsesFailoverExhausted(c, fs.LastFailoverErr, streamStarted)
				} else {
					h.responsesErrorResponse(c, http.StatusBadGateway, "server_error", "All available accounts exhausted")
				}
				return
			}
		}
		account := selection.Account
		setOpsSelectedAccount(c, account.ID, account.Platform)

		// Anthropic Messages 风格的 Coding Plan 账号无法承接 Codex /v1/responses 流量，
		// 把它从本次选号循环中剔除并继续 failover，让其它 Chat Completions 账号有机会接管；
		// 只有当所有候选账号都是 Anthropic Messages 时，才在循环耗尽后由 selection 错误分支
		// 返回 No available accounts。
		if service.IsCodingPlanAnthropicMessagesAccount(account) {
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			reqLog.Warn("gateway.responses.skip_anthropic_messages_account",
				zap.Int64("account_id", account.ID),
				zap.String("platform", account.Platform),
			)
			fs.FailedAccountIDs[account.ID] = struct{}{}
			if fs.SwitchCount >= fs.MaxSwitches {
				h.responsesErrorResponse(c, http.StatusNotImplemented, "unsupported_platform", "该分组下没有可用的 Chat Completions 账号；Anthropic Messages 账号无法承接 Codex /v1/responses 流量，请改用 Chat Completions 格式账号或将其放到 /v1/messages 入口")
				return
			}
			fs.SwitchCount++
			continue
		}

		// 4. Acquire account concurrency slot
		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				markOpsRoutingCapacityLimited(c)
				h.responsesErrorResponse(c, http.StatusServiceUnavailable, "api_error", "No available accounts")
				return
			}
			accountReleaseFunc, err = h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
				c,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
				selection.WaitPlan.Timeout,
				reqStream,
				&streamStarted,
			)
			if err != nil {
				reqLog.Warn("gateway.coding_plan.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				h.handleConcurrencyError(c, err, "account", streamStarted)
				return
			}
		}
		accountReleaseFunc = wrapReleaseOnDone(c.Request.Context(), accountReleaseFunc)

		// 5. Force model mapping
		// 国产 Coding Plan 模型与 OpenAI gpt-*/o*/codex-* 没有兼容关系，
		// 必须由账号或渠道级 model_mapping 显式声明，否则 400 提示用户配置。
		// 这样做有两个目的：
		//   - 不让国产真实模型“伪装成 GPT”进入计费/统计/缓存命名空间
		//   - 不在 handler 内置 gpt-* → kimi-k2.7-code/glm-5.2 这类隐式映射，
		//     用户以为支持 gpt-5.5 但其实根本不是 OpenAI 在算
		mappedModel := reqModel
		if channelMapping.Mapped {
			mappedModel = channelMapping.MappedModel
		}
		mappedModel = account.GetMappedModel(mappedModel)

		if mappedModel == reqModel && (strings.HasPrefix(reqModel, "gpt-") || strings.HasPrefix(reqModel, "o") || strings.HasPrefix(reqModel, "codex-")) {
			if accountReleaseFunc != nil {
				accountReleaseFunc()
			}
			h.responsesErrorResponse(c, http.StatusBadRequest, "invalid_request_error",
				fmt.Sprintf("模型 %q 不能直接路由到 %s（国产 Coding Plan）账号。请在该账号或所在分组上配置 model_mapping，把该 OpenAI 模型映射到此供应商的真实上游模型名（例如 kimi-k2-turbo-preview / glm-4.6 等）。", reqModel, account.Platform))
			return
		}

		wireAPI := account.GetCredential("wire_api")
		if wireAPI == "" {
			wireAPI = account.GetCredential("api_format")
		}
		if wireAPI == "" {
			wireAPI = "openai_chat"
		}
		responsesSupport := account.GetExtraString("responses_support")
		if responsesSupport == "" {
			responsesSupport = "via_chat_completions"
		}

		reqLog = reqLog.With(
			zap.String("requested_model", reqModel),
			zap.String("upstream_model", mappedModel),
			zap.String("platform", account.Platform),
			zap.String("wire_api", wireAPI),
			zap.String("responses_support", responsesSupport),
		)

		// 6. Forward request
		writerSizeBeforeForward := c.Writer.Size()
		forwardBody := body
		if mappedModel != reqModel {
			forwardBody = h.gatewayService.ReplaceModelInBody(body, mappedModel)
		}
		
		var result *service.ForwardResult
		result, err = h.gatewayService.ForwardCodexResponsesViaChatCompletions(requestCtx, c, account, forwardBody, parsedReq)
		
		if accountReleaseFunc != nil {
			accountReleaseFunc()
		}

		if err != nil {
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				// Can't failover if streaming content already sent
				if c.Writer.Size() != writerSizeBeforeForward {
					h.handleResponsesFailoverExhausted(c, failoverErr, true)
					return
				}
				action := fs.HandleFailoverError(requestCtx, h.gatewayService, account.ID, account.Platform, failoverErr)
				switch action {
				case FailoverContinue:
					continue
				case FailoverExhausted:
					h.handleResponsesFailoverExhausted(c, fs.LastFailoverErr, streamStarted)
					return
				case FailoverCanceled:
					return
				}
			}
			upstreamErrorAlreadyCommunicated := gatewayForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
			wroteFallback := false
			if !upstreamErrorAlreadyCommunicated {
				wroteFallback = h.ensureForwardErrorResponse(c, streamStarted)
			}
			reqLog.Error("gateway.responses.forward_failed",
				zap.Int64("account_id", account.ID),
				zap.Bool("fallback_error_response_written", wroteFallback),
				zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
				zap.Error(err),
			)
			return
		}

		// 6. Record usage
		userAgent := c.GetHeader("User-Agent")
		clientIP := ip.GetClientIP(c)
		requestPayloadHash := service.HashUsageRequestPayload(body)
		inboundEndpoint := GetInboundEndpoint(c)
		upstreamEndpoint := GetUpstreamEndpoint(c, account.Platform)

		quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
		h.submitUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
			if err := h.gatewayService.RecordUsage(ctx, &service.RecordUsageInput{
				Result:             result,
				QuotaPlatform:      quotaPlatform,
				APIKey:             apiKey,
				User:               apiKey.User,
				Account:            account,
				Subscription:       subscription,
				InboundEndpoint:    inboundEndpoint,
				UpstreamEndpoint:   upstreamEndpoint,
				UserAgent:          userAgent,
				IPAddress:          clientIP,
				RequestPayloadHash: requestPayloadHash,
				APIKeyService:      h.apiKeyService,
				ChannelUsageFields: channelMapping.ToUsageFields(reqModel, result.UpstreamModel),
			}); err != nil {
				reqLog.Error("gateway.responses.record_usage_failed",
					zap.Int64("account_id", account.ID),
					zap.Error(err),
				)
			}
		})
		return
	}
}

// responsesErrorResponse writes an error in OpenAI Responses API format.
