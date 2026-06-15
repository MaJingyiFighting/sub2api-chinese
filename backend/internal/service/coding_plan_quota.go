package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type CodingPlanProvider string

const (
	CodingPlanProviderKimi       CodingPlanProvider = "kimi"
	CodingPlanProviderZhipu      CodingPlanProvider = "zhipu"
	CodingPlanProviderMiniMax    CodingPlanProvider = "minimax"
	CodingPlanProviderVolcengine CodingPlanProvider = "volcengine"
	CodingPlanProviderMiMo       CodingPlanProvider = "mimo"
)

func IsCodingPlanPlatform(platform string) bool {
	switch platform {
	case string(CodingPlanProviderKimi),
		string(CodingPlanProviderZhipu),
		string(CodingPlanProviderMiniMax),
		string(CodingPlanProviderVolcengine),
		string(CodingPlanProviderMiMo):
		return true
	default:
		return false
	}
}

const (
	CodingPlanProbeStatusSupported    = "supported"
	CodingPlanProbeStatusUnsupported  = "unsupported"
	CodingPlanProbeStatusExperimental = "experimental"

	codingPlanQuotaProbeCacheTTL = 10 * time.Minute
	codingPlanQuotaStaleAfter    = 2 * time.Hour
)

var ErrCodingPlanQuotaProbeUnsupported = errors.New("quota probe is not implemented because no verified public endpoint is available")

type CodingPlanQuotaSnapshot struct {
	Provider string `json:"provider,omitempty"`

	FiveHourUsedPercent        *float64   `json:"five_hour_used_percent,omitempty"`
	FiveHourResetAt            *time.Time `json:"five_hour_reset_at,omitempty"`
	FiveHourResetAfterSeconds  *int64     `json:"five_hour_reset_after_seconds,omitempty"`
	WeeklyUsedPercent          *float64   `json:"weekly_used_percent,omitempty"`
	WeeklyResetAt              *time.Time `json:"weekly_reset_at,omitempty"`
	WeeklyResetAfterSeconds    *int64     `json:"weekly_reset_after_seconds,omitempty"`
	PlanName                   *string    `json:"plan_name,omitempty"`
	AccountStatus              *string    `json:"account_status,omitempty"`
	QuotaProbeStatus           string     `json:"quota_probe_status,omitempty"`
	Raw                        map[string]any
	UpdatedAt                  time.Time `json:"updated_at,omitempty"`
	Source                     string    `json:"source,omitempty"`
	Success                    bool      `json:"success"`
	ErrorMessage               string    `json:"error_message,omitempty"`
	HTTPStatus                 int       `json:"http_status,omitempty"`
	CredentialExpired          bool      `json:"credential_expired,omitempty"`
	TemporaryUnschedulableHint bool      `json:"temporary_unschedulable_hint,omitempty"`
}

type CodingPlanQuotaProbe interface {
	Provider() CodingPlanProvider
	Detect(baseURL string) bool
	Probe(ctx context.Context, account *Account) (*CodingPlanQuotaSnapshot, error)
}

type ProviderErrorDecision struct {
	Retryable              bool
	ShouldFailover         bool
	QuotaExhausted         bool
	RateLimited            bool
	Overloaded             bool
	AuthFailed             bool
	TempUnschedulableUntil *time.Time
	Reason                 string
}

type codingPlanHTTPProbe struct {
	provider CodingPlanProvider
	client   *http.Client
}

func ProbeCodingPlanQuota(ctx context.Context, account *Account) (*CodingPlanQuotaSnapshot, error) {
	probe := DetectCodingPlanQuotaProbe(account)
	if probe == nil {
		provider := ResolveCodingPlanProvider(account)
		if provider == "" {
			return nil, fmt.Errorf("coding plan provider is not configured")
		}
		return unsupportedCodingPlanQuotaSnapshot(provider, ErrCodingPlanQuotaProbeUnsupported.Error()), ErrCodingPlanQuotaProbeUnsupported
	}
	return probe.Probe(ctx, account)
}

func DetectCodingPlanQuotaProbe(account *Account) CodingPlanQuotaProbe {
	if account == nil {
		return nil
	}
	baseURL := accountCodingPlanBaseURL(account)
	explicit := ResolveCodingPlanProvider(account)
	probes := []CodingPlanQuotaProbe{
		NewKimiCodingPlanProbe(nil),
		NewZhipuCodingPlanProbe(nil),
		NewMiniMaxCodingPlanProbe(nil),
		NewVolcengineCodingPlanProbe(nil),
		NewMiMoCodingPlanProbe(nil),
	}
	for _, probe := range probes {
		if explicit != "" && probe.Provider() == explicit {
			return probe
		}
	}
	for _, probe := range probes {
		if probe.Detect(baseURL) {
			return probe
		}
	}
	return nil
}

func ResolveCodingPlanProvider(account *Account) CodingPlanProvider {
	if account == nil {
		return ""
	}
	if provider := normalizeCodingPlanProvider(account.GetExtraString("coding_plan_provider")); provider != "" {
		return provider
	}
	return DetectCodingPlanProviderFromBaseURL(accountCodingPlanBaseURL(account))
}

func DetectCodingPlanProviderFromBaseURL(baseURL string) CodingPlanProvider {
	normalized := strings.ToLower(strings.TrimSpace(baseURL))
	switch {
	case strings.Contains(normalized, "api.kimi.com/coding"), strings.Contains(normalized, "api.moonshot.cn"):
		return CodingPlanProviderKimi
	case strings.Contains(normalized, "open.bigmodel.cn"), strings.Contains(normalized, "bigmodel.cn"), strings.Contains(normalized, "api.z.ai"):
		return CodingPlanProviderZhipu
	case strings.Contains(normalized, "api.minimaxi.com"), strings.Contains(normalized, "api.minimax.io"):
		return CodingPlanProviderMiniMax
	case strings.Contains(normalized, "volces.com"), strings.Contains(normalized, "volcengine"), strings.Contains(normalized, "ark.cn-beijing.volces.com"), strings.Contains(normalized, "ark.volces.com"):
		return CodingPlanProviderVolcengine
	case strings.Contains(normalized, "mimo"), strings.Contains(normalized, "mi.com"), strings.Contains(normalized, "xiaomi"):
		return CodingPlanProviderMiMo
	default:
		return ""
	}
}

func normalizeCodingPlanProvider(value string) CodingPlanProvider {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(CodingPlanProviderKimi), "moonshot", "kimi_for_coding":
		return CodingPlanProviderKimi
	case string(CodingPlanProviderZhipu), "glm", "bigmodel", "zai", "z.ai":
		return CodingPlanProviderZhipu
	case string(CodingPlanProviderMiniMax), "mini-max", "minimax-coding":
		return CodingPlanProviderMiniMax
	case string(CodingPlanProviderVolcengine), "volcano", "ark", "doubao":
		return CodingPlanProviderVolcengine
	case string(CodingPlanProviderMiMo), "xiaomi":
		return CodingPlanProviderMiMo
	default:
		return ""
	}
}

func IsCodingPlanAccount(account *Account) bool {
	return ResolveCodingPlanProvider(account) != ""
}

func GetCodingPlanAPIFormat(account *Account) string {
	if account == nil {
		return ""
	}
	if account.Credentials != nil {
		if fmtVal, ok := account.Credentials["api_format"].(string); ok && fmtVal != "" {
			return fmtVal
		}
	}
	return ""
}

func IsCodingPlanChatCompletionsAccount(account *Account) bool {
	if !IsCodingPlanAccount(account) {
		return false
	}
	format := GetCodingPlanAPIFormat(account)
	wireAPI := strings.TrimSpace(account.GetCredential("wire_api"))
	responsesSupport := account.GetExtraString("responses_support")
	return format == "chat_completions" || format == "openai_chat" ||
		wireAPI == "chat_completions" || wireAPI == "openai_chat" ||
		responsesSupport == "via_chat_completions"
}

func IsCodingPlanAnthropicMessagesAccount(account *Account) bool {
	if !IsCodingPlanAccount(account) {
		return false
	}
	format := GetCodingPlanAPIFormat(account)
	wireAPI := strings.TrimSpace(account.GetCredential("wire_api"))
	responsesSupport := account.GetExtraString("responses_support")
	return format == "anthropic_messages" ||
		wireAPI == "anthropic_messages" ||
		responsesSupport == "via_anthropic_messages"
}

func accountCodingPlanBaseURL(account *Account) string {
	if account == nil {
		return ""
	}
	if account.Type == AccountTypeAPIKey || account.Type == AccountTypeUpstream {
		if baseURL := strings.TrimSpace(account.GetCredential("base_url")); baseURL != "" {
			return baseURL
		}
	}
	return strings.TrimSpace(account.GetOpenAIBaseURL())
}

func accountCodingPlanQuotaBaseURL(account *Account) string {
	if account == nil {
		return ""
	}
	if quotaURL := strings.TrimSpace(account.GetExtraString("quota_base_url")); quotaURL != "" {
		return quotaURL
	}
	return accountCodingPlanBaseURL(account)
}

func accountCodingPlanAPIKey(account *Account) string {
	if account == nil {
		return ""
	}
	if key := strings.TrimSpace(account.GetCredential("api_key")); key != "" {
		return key
	}
	return strings.TrimSpace(account.GetOpenAIApiKey())
}

func NewKimiCodingPlanProbe(client *http.Client) CodingPlanQuotaProbe {
	return &codingPlanHTTPProbe{provider: CodingPlanProviderKimi, client: client}
}

func NewZhipuCodingPlanProbe(client *http.Client) CodingPlanQuotaProbe {
	return &codingPlanHTTPProbe{provider: CodingPlanProviderZhipu, client: client}
}

func NewMiniMaxCodingPlanProbe(client *http.Client) CodingPlanQuotaProbe {
	return &codingPlanHTTPProbe{provider: CodingPlanProviderMiniMax, client: client}
}

func NewVolcengineCodingPlanProbe(client *http.Client) CodingPlanQuotaProbe {
	return &codingPlanHTTPProbe{provider: CodingPlanProviderVolcengine, client: client}
}

func NewMiMoCodingPlanProbe(client *http.Client) CodingPlanQuotaProbe {
	return &codingPlanHTTPProbe{provider: CodingPlanProviderMiMo, client: client}
}

func (p *codingPlanHTTPProbe) Provider() CodingPlanProvider {
	if p == nil {
		return ""
	}
	return p.provider
}

func (p *codingPlanHTTPProbe) Detect(baseURL string) bool {
	return DetectCodingPlanProviderFromBaseURL(baseURL) == p.Provider()
}

func (p *codingPlanHTTPProbe) Probe(ctx context.Context, account *Account) (*CodingPlanQuotaSnapshot, error) {
	switch p.Provider() {
	case CodingPlanProviderKimi:
		return p.probeKimi(ctx, account)
	case CodingPlanProviderZhipu:
		return p.probeZhipu(ctx, account)
	case CodingPlanProviderMiniMax:
		return p.probeMiniMax(ctx, account)
	case CodingPlanProviderVolcengine:
		return p.probeUnsupportedOrExperimental(ctx, account, "volcengine_quota_probe_url", "volcengine_quota_probe_auth_mode")
	case CodingPlanProviderMiMo:
		return p.probeUnsupportedOrExperimental(ctx, account, "mimo_quota_probe_url", "mimo_quota_probe_auth_mode")
	default:
		return nil, fmt.Errorf("unsupported coding plan provider: %s", p.Provider())
	}
}

func (p *codingPlanHTTPProbe) httpClient() *http.Client {
	if p != nil && p.client != nil {
		return p.client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (p *codingPlanHTTPProbe) probeKimi(ctx context.Context, account *Account) (*CodingPlanQuotaSnapshot, error) {
	baseURL := accountCodingPlanQuotaBaseURL(account)
	endpoint := "https://api.kimi.com/coding/v1/usages"
	if baseURL != "" && !strings.Contains(baseURL, "api.kimi.com/coding") && !strings.Contains(baseURL, "api.moonshot.cn") {
		endpoint = strings.TrimRight(baseURL, "/") + "/v1/usages"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	apiKey := accountCodingPlanAPIKey(account)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Accept", "application/json")

	body, status, err := p.doProbe(req)
	if err != nil {
		return codingPlanNetworkErrorSnapshot(CodingPlanProviderKimi, err), err
	}
	if status < 200 || status >= 300 {
		return parseCodingPlanHTTPError(CodingPlanProviderKimi, status, body), nil
	}
	snapshot, err := ParseKimiCodingPlanQuota(body, time.Now())
	if err != nil {
		return codingPlanParseErrorSnapshot(CodingPlanProviderKimi, body, err), err
	}
	return snapshot, nil
}

func (p *codingPlanHTTPProbe) probeZhipu(ctx context.Context, account *Account) (*CodingPlanQuotaSnapshot, error) {
	endpoint := zhipuCodingPlanQuotaEndpoint(accountCodingPlanQuotaBaseURL(account))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", accountCodingPlanAPIKey(account))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "en-US,en")

	body, status, err := p.doProbe(req)
	if err != nil {
		return codingPlanNetworkErrorSnapshot(CodingPlanProviderZhipu, err), err
	}
	if status < 200 || status >= 300 {
		return parseCodingPlanHTTPError(CodingPlanProviderZhipu, status, body), nil
	}
	snapshot, err := ParseZhipuCodingPlanQuota(body, time.Now())
	if err != nil {
		return codingPlanParseErrorSnapshot(CodingPlanProviderZhipu, body, err), err
	}
	return snapshot, nil
}

func (p *codingPlanHTTPProbe) probeMiniMax(ctx context.Context, account *Account) (*CodingPlanQuotaSnapshot, error) {
	endpoint := miniMaxCodingPlanQuotaEndpoint(accountCodingPlanQuotaBaseURL(account))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accountCodingPlanAPIKey(account))
	req.Header.Set("Content-Type", "application/json")

	body, status, err := p.doProbe(req)
	if err != nil {
		return codingPlanNetworkErrorSnapshot(CodingPlanProviderMiniMax, err), err
	}
	if status < 200 || status >= 300 {
		return parseCodingPlanHTTPError(CodingPlanProviderMiniMax, status, body), nil
	}
	snapshot, err := ParseMiniMaxCodingPlanQuota(body, time.Now())
	if err != nil {
		return codingPlanParseErrorSnapshot(CodingPlanProviderMiniMax, body, err), err
	}
	return snapshot, nil
}

func (p *codingPlanHTTPProbe) probeUnsupportedOrExperimental(ctx context.Context, account *Account, urlKey, authModeKey string) (*CodingPlanQuotaSnapshot, error) {
	provider := p.Provider()
	probeURL := strings.TrimSpace(account.GetExtraString(urlKey))
	if probeURL == "" {
		snapshot := unsupportedCodingPlanQuotaSnapshot(provider, ErrCodingPlanQuotaProbeUnsupported.Error())
		snapshot.QuotaProbeStatus = CodingPlanProbeStatusExperimental
		return snapshot, ErrCodingPlanQuotaProbeUnsupported
	}
	if err := validateExperimentalProbeURL(probeURL); err != nil {
		snapshot := unsupportedCodingPlanQuotaSnapshot(provider, err.Error())
		snapshot.QuotaProbeStatus = CodingPlanProbeStatusExperimental
		return snapshot, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
	if err != nil {
		return nil, err
	}
	authMode := strings.ToLower(strings.TrimSpace(account.GetExtraString(authModeKey)))
	apiKey := accountCodingPlanAPIKey(account)
	switch authMode {
	case "raw":
		req.Header.Set("Authorization", apiKey)
	case "none":
	default:
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	body, status, err := p.doProbe(req)
	if err != nil {
		snapshot := codingPlanNetworkErrorSnapshot(provider, err)
		snapshot.QuotaProbeStatus = CodingPlanProbeStatusExperimental
		return snapshot, err
	}
	snapshot := parseCodingPlanHTTPError(provider, status, body)
	snapshot.QuotaProbeStatus = CodingPlanProbeStatusExperimental
	snapshot.Raw = map[string]any{"body": string(body)}
	if status >= 200 && status < 300 {
		snapshot.Success = true
		snapshot.ErrorMessage = ""
		snapshot.Source = "active_probe"
	}
	return snapshot, nil
}

func (p *codingPlanHTTPProbe) doProbe(req *http.Request) ([]byte, int, error) {
	resp, err := p.httpClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if readErr != nil {
		return nil, resp.StatusCode, readErr
	}
	return body, resp.StatusCode, nil
}

func validateExperimentalProbeURL(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid experimental quota probe url")
	}
	if parsed.Scheme != "https" {
		return fmt.Errorf("experimental quota probe url must use https")
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return fmt.Errorf("experimental quota probe url cannot be localhost")
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("experimental quota probe url cannot use a private or local IP")
		}
	}
	return nil
}

func zhipuCodingPlanQuotaEndpoint(baseURL string) string {
	if strings.Contains(strings.ToLower(baseURL), "api.z.ai") {
		return "https://api.z.ai/api/monitor/usage/quota/limit"
	}
	return "https://open.bigmodel.cn/api/monitor/usage/quota/limit"
}

func miniMaxCodingPlanQuotaEndpoint(baseURL string) string {
	if strings.Contains(strings.ToLower(baseURL), "api.minimax.io") {
		return "https://api.minimax.io/v1/api/openplatform/coding_plan/remains"
	}
	return "https://api.minimaxi.com/v1/api/openplatform/coding_plan/remains"
}

func ParseKimiCodingPlanQuota(body []byte, now time.Time) (*CodingPlanQuotaSnapshot, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	snapshot := newCodingPlanQuotaSnapshot(CodingPlanProviderKimi, now, "active_probe")
	snapshot.Raw = root

	if limits, ok := root["limits"].([]any); ok {
		for _, item := range limits {
			entry, _ := item.(map[string]any)
			detail, _ := entry["detail"].(map[string]any)
			limit := parseExtraFloat64(detail["limit"])
			remaining := parseExtraFloat64(detail["remaining"])
			if limit <= 0 {
				continue
			}
			usedPercent := usedPercentFromLimitRemaining(limit, remaining)
			snapshot.FiveHourUsedPercent = &usedPercent
			if resetAt := parseCodingPlanResetTime(detail["resetTime"], now); resetAt != nil {
				snapshot.FiveHourResetAt = resetAt
				seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
				snapshot.FiveHourResetAfterSeconds = &seconds
			}
			break
		}
	}

	if usage, ok := root["usage"].(map[string]any); ok {
		limit := parseExtraFloat64(usage["limit"])
		remaining := parseExtraFloat64(usage["remaining"])
		if limit > 0 {
			usedPercent := usedPercentFromLimitRemaining(limit, remaining)
			snapshot.WeeklyUsedPercent = &usedPercent
			if resetAt := parseCodingPlanResetTime(usage["resetTime"], now); resetAt != nil {
				snapshot.WeeklyResetAt = resetAt
				seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
				snapshot.WeeklyResetAfterSeconds = &seconds
			}
		}
	}

	return snapshot, nil
}

func ParseZhipuCodingPlanQuota(body []byte, now time.Time) (*CodingPlanQuotaSnapshot, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	snapshot := newCodingPlanQuotaSnapshot(CodingPlanProviderZhipu, now, "active_probe")
	snapshot.Raw = root

	data, _ := root["data"].(map[string]any)
	if level, ok := data["level"].(string); ok && strings.TrimSpace(level) != "" {
		plan := strings.TrimSpace(level)
		snapshot.PlanName = &plan
	}
	limits, _ := data["limits"].([]any)
	tokenLimits := make([]map[string]any, 0, len(limits))
	for _, item := range limits {
		entry, _ := item.(map[string]any)
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(entry["type"])), "TOKENS_LIMIT") {
			tokenLimits = append(tokenLimits, entry)
		}
	}

	for _, entry := range tokenLimits {
		unit := int(parseExtraFloat64(entry["unit"]))
		usedPercent := parseExtraFloat64(entry["percentage"])
		resetAt := parseCodingPlanResetTime(entry["nextResetTime"], now)
		switch unit {
		case 3:
			snapshot.FiveHourUsedPercent = &usedPercent
			if resetAt != nil {
				snapshot.FiveHourResetAt = resetAt
				seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
				snapshot.FiveHourResetAfterSeconds = &seconds
			}
		case 6:
			snapshot.WeeklyUsedPercent = &usedPercent
			if resetAt != nil {
				snapshot.WeeklyResetAt = resetAt
				seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
				snapshot.WeeklyResetAfterSeconds = &seconds
			}
		}
	}
	if len(tokenLimits) == 1 && snapshot.FiveHourUsedPercent == nil && snapshot.WeeklyUsedPercent == nil {
		entry := tokenLimits[0]
		usedPercent := parseExtraFloat64(entry["percentage"])
		snapshot.FiveHourUsedPercent = &usedPercent
		if resetAt := parseCodingPlanResetTime(entry["nextResetTime"], now); resetAt != nil {
			snapshot.FiveHourResetAt = resetAt
			seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
			snapshot.FiveHourResetAfterSeconds = &seconds
		}
	}

	if successRaw, ok := root["success"]; ok {
		if success, ok := successRaw.(bool); ok && !success {
			snapshot.Success = false
			snapshot.ErrorMessage = strings.TrimSpace(fmt.Sprint(root["message"]))
			if snapshot.ErrorMessage == "" {
				snapshot.ErrorMessage = "zhipu quota API returned success=false"
			}
			return snapshot, errors.New(snapshot.ErrorMessage)
		}
	}
	return snapshot, nil
}

func ParseMiniMaxCodingPlanQuota(body []byte, now time.Time) (*CodingPlanQuotaSnapshot, error) {
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, err
	}
	snapshot := newCodingPlanQuotaSnapshot(CodingPlanProviderMiniMax, now, "active_probe")
	snapshot.Raw = root

	baseResp, _ := root["base_resp"].(map[string]any)
	if code := parseExtraFloat64(baseResp["status_code"]); code != 0 {
		msg := strings.TrimSpace(fmt.Sprint(baseResp["status_msg"]))
		if msg == "" {
			msg = strings.TrimSpace(fmt.Sprint(baseResp["message"]))
		}
		if msg == "" {
			msg = fmt.Sprintf("minimax business error: %.0f", code)
		}
		snapshot.Success = false
		snapshot.ErrorMessage = msg
		return snapshot, errors.New(msg)
	}

	modelRemains, _ := root["model_remains"].([]any)
	for _, item := range modelRemains {
		entry, _ := item.(map[string]any)
		if !strings.EqualFold(strings.TrimSpace(fmt.Sprint(entry["model_name"])), "general") {
			continue
		}
		fiveHourUsed := 100 - parseExtraFloat64(entry["current_interval_remaining_percent"])
		fiveHourUsed = clampPercent(fiveHourUsed)
		snapshot.FiveHourUsedPercent = &fiveHourUsed
		if resetAt := parseCodingPlanResetTime(entry["end_time"], now); resetAt != nil {
			snapshot.FiveHourResetAt = resetAt
			seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
			snapshot.FiveHourResetAfterSeconds = &seconds
		}
		if int(parseExtraFloat64(entry["current_weekly_status"])) == 1 {
			weeklyUsed := 100 - parseExtraFloat64(entry["current_weekly_remaining_percent"])
			weeklyUsed = clampPercent(weeklyUsed)
			snapshot.WeeklyUsedPercent = &weeklyUsed
			if resetAt := parseCodingPlanResetTime(entry["weekly_end_time"], now); resetAt != nil {
				snapshot.WeeklyResetAt = resetAt
				seconds := int64(math.Max(0, resetAt.Sub(now).Seconds()))
				snapshot.WeeklyResetAfterSeconds = &seconds
			}
		}
		break
	}
	return snapshot, nil
}

func newCodingPlanQuotaSnapshot(provider CodingPlanProvider, now time.Time, source string) *CodingPlanQuotaSnapshot {
	if now.IsZero() {
		now = time.Now()
	}
	return &CodingPlanQuotaSnapshot{
		Provider:         string(provider),
		QuotaProbeStatus: CodingPlanProbeStatusSupported,
		UpdatedAt:        now.UTC(),
		Source:           source,
		Success:          true,
	}
}

func unsupportedCodingPlanQuotaSnapshot(provider CodingPlanProvider, message string) *CodingPlanQuotaSnapshot {
	now := time.Now().UTC()
	return &CodingPlanQuotaSnapshot{
		Provider:         string(provider),
		QuotaProbeStatus: CodingPlanProbeStatusUnsupported,
		UpdatedAt:        now,
		Source:           "active_probe",
		Success:          false,
		ErrorMessage:     message,
	}
}

func codingPlanNetworkErrorSnapshot(provider CodingPlanProvider, err error) *CodingPlanQuotaSnapshot {
	snapshot := newCodingPlanQuotaSnapshot(provider, time.Now(), "active_probe")
	snapshot.Success = false
	snapshot.ErrorMessage = fmt.Sprintf("quota probe request failed: %v", err)
	return snapshot
}

func codingPlanParseErrorSnapshot(provider CodingPlanProvider, body []byte, err error) *CodingPlanQuotaSnapshot {
	snapshot := newCodingPlanQuotaSnapshot(provider, time.Now(), "active_probe")
	snapshot.Success = false
	snapshot.ErrorMessage = fmt.Sprintf("quota probe parse failed: %v", err)
	snapshot.Raw = map[string]any{"body": string(bytes.TrimSpace(body))}
	return snapshot
}

func parseCodingPlanHTTPError(provider CodingPlanProvider, status int, body []byte) *CodingPlanQuotaSnapshot {
	snapshot := newCodingPlanQuotaSnapshot(provider, time.Now(), "active_probe")
	snapshot.Success = false
	snapshot.HTTPStatus = status
	bodyText := strings.TrimSpace(string(body))
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		snapshot.CredentialExpired = true
		snapshot.AccountStatus = codingPlanStringPtr("credential_expired")
		snapshot.ErrorMessage = fmt.Sprintf("quota probe auth failed: HTTP %d", status)
	case http.StatusTooManyRequests:
		snapshot.TemporaryUnschedulableHint = true
		snapshot.ErrorMessage = "quota probe rate limited: HTTP 429"
	default:
		if status >= 500 {
			snapshot.TemporaryUnschedulableHint = true
		}
		snapshot.ErrorMessage = fmt.Sprintf("quota probe returned HTTP %d", status)
	}
	if bodyText != "" {
		snapshot.Raw = map[string]any{"body": bodyText}
	}
	return snapshot
}

func buildCodingPlanQuotaExtraUpdates(snapshot *CodingPlanQuotaSnapshot) map[string]any {
	if snapshot == nil {
		return nil
	}
	updatedAt := snapshot.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	updates := map[string]any{
		"coding_plan_provider":         snapshot.Provider,
		"coding_plan_probe_status":     firstNonEmptyCodingPlan(snapshot.QuotaProbeStatus, CodingPlanProbeStatusSupported),
		"coding_plan_usage_updated_at": updatedAt.UTC().Format(time.RFC3339),
		"coding_plan_source":           snapshot.Source,
		"coding_plan_success":          snapshot.Success,
	}
	if snapshot.FiveHourUsedPercent != nil {
		updates["coding_plan_5h_used_percent"] = *snapshot.FiveHourUsedPercent
	}
	if snapshot.FiveHourResetAt != nil {
		updates["coding_plan_5h_reset_at"] = snapshot.FiveHourResetAt.UTC().Format(time.RFC3339)
	}
	if snapshot.FiveHourResetAfterSeconds != nil {
		updates["coding_plan_5h_reset_after_seconds"] = *snapshot.FiveHourResetAfterSeconds
	}
	if snapshot.WeeklyUsedPercent != nil {
		updates["coding_plan_weekly_used_percent"] = *snapshot.WeeklyUsedPercent
	}
	if snapshot.WeeklyResetAt != nil {
		updates["coding_plan_weekly_reset_at"] = snapshot.WeeklyResetAt.UTC().Format(time.RFC3339)
	}
	if snapshot.WeeklyResetAfterSeconds != nil {
		updates["coding_plan_weekly_reset_after_seconds"] = *snapshot.WeeklyResetAfterSeconds
	}
	if snapshot.PlanName != nil {
		updates["coding_plan_plan_name"] = *snapshot.PlanName
	}
	if snapshot.AccountStatus != nil {
		updates["coding_plan_account_status"] = *snapshot.AccountStatus
	}
	if snapshot.ErrorMessage != "" {
		updates["coding_plan_probe_error"] = snapshot.ErrorMessage
	} else {
		updates["coding_plan_probe_error"] = ""
	}
	return updates
}

func buildUsageInfoFromCodingPlanSnapshot(snapshot *CodingPlanQuotaSnapshot, now time.Time) *UsageInfo {
	if snapshot == nil {
		return nil
	}
	updatedAt := snapshot.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = now
	}
	info := &UsageInfo{
		Source:    snapshot.Source,
		UpdatedAt: &updatedAt,
	}
	if !snapshot.Success && snapshot.ErrorMessage != "" {
		info.Error = snapshot.ErrorMessage
	}
	if snapshot.FiveHourUsedPercent != nil {
		info.FiveHour = &UsageProgress{
			Utilization: *snapshot.FiveHourUsedPercent,
			ResetsAt:    snapshot.FiveHourResetAt,
		}
		if snapshot.FiveHourResetAt != nil {
			info.FiveHour.RemainingSeconds = remainingSeconds(*snapshot.FiveHourResetAt, now)
		}
	}
	if snapshot.WeeklyUsedPercent != nil {
		info.SevenDay = &UsageProgress{
			Utilization: *snapshot.WeeklyUsedPercent,
			ResetsAt:    snapshot.WeeklyResetAt,
		}
		if snapshot.WeeklyResetAt != nil {
			info.SevenDay.RemainingSeconds = remainingSeconds(*snapshot.WeeklyResetAt, now)
		}
	}
	return info
}

func buildCodingPlanUsageProgressFromExtra(extra map[string]any, window string, now time.Time) *UsageProgress {
	if len(extra) == 0 {
		return nil
	}
	var usedKey, resetAtKey, resetAfterKey string
	switch window {
	case "5h":
		usedKey = "coding_plan_5h_used_percent"
		resetAtKey = "coding_plan_5h_reset_at"
		resetAfterKey = "coding_plan_5h_reset_after_seconds"
	case "weekly":
		usedKey = "coding_plan_weekly_used_percent"
		resetAtKey = "coding_plan_weekly_reset_at"
		resetAfterKey = "coding_plan_weekly_reset_after_seconds"
	default:
		return nil
	}
	used, ok := resolveAccountExtraNumber(extra, usedKey)
	if !ok {
		return nil
	}
	progress := &UsageProgress{Utilization: used}
	if resetAtRaw, ok := extra[resetAtKey]; ok {
		if resetAt, err := parseTime(fmt.Sprint(resetAtRaw)); err == nil {
			progress.ResetsAt = &resetAt
			progress.RemainingSeconds = remainingSeconds(resetAt, now)
			return progress
		}
	}
	resetAfter := parseExtraInt(extra[resetAfterKey])
	if resetAfter > 0 {
		base := now
		if updatedRaw, ok := extra["coding_plan_usage_updated_at"]; ok {
			if updatedAt, err := parseTime(fmt.Sprint(updatedRaw)); err == nil {
				base = updatedAt
			}
		}
		resetAt := base.Add(time.Duration(resetAfter) * time.Second)
		progress.ResetsAt = &resetAt
		progress.RemainingSeconds = remainingSeconds(resetAt, now)
	}
	return progress
}

func codingPlanQuotaUtilization(account *Account, now time.Time) float64 {
	if account == nil || len(account.Extra) == 0 {
		return 0
	}
	maxUsed := 0.0
	for _, key := range []string{"coding_plan_5h_used_percent", "coding_plan_weekly_used_percent", "codex_5h_used_percent", "codex_7d_used_percent"} {
		if used, ok := resolveAccountExtraNumber(account.Extra, key); ok && used > maxUsed {
			maxUsed = used
		}
	}
	if maxUsed <= 0 {
		return 0
	}
	if codingPlanSnapshotStaleForPause(account.Extra, now) {
		return 0
	}
	return clamp01(maxUsed / 100)
}

func shouldAutoPauseCodingPlanAccountByQuota(ctx context.Context, account *Account) (bool, openAIQuotaAutoPauseDecision) {
	if account == nil || !IsCodingPlanAccount(account) {
		return false, openAIQuotaAutoPauseDecision{}
	}
	disabled5h := resolveAccountExtraBool(account.Extra, "auto_pause_5h_disabled")
	disabled7d := resolveAccountExtraBool(account.Extra, "auto_pause_7d_disabled")
	threshold5h, threshold7d := resolveOpenAIQuotaAutoPauseThresholds(ctx, account)
	if threshold5h <= 0 {
		threshold5h = 0.98
	}
	if threshold7d <= 0 {
		threshold7d = 0.98
	}
	now := time.Now()
	if !disabled5h && threshold5h > 0 {
		if utilization, ok := resolveCodingPlanQuotaUtilization(account.Extra, "5h", now); ok && utilization >= threshold5h {
			return true, openAIQuotaAutoPauseDecision{window: "5h", threshold: threshold5h, utilization: utilization}
		}
	}
	if !disabled7d && threshold7d > 0 {
		if utilization, ok := resolveCodingPlanQuotaUtilization(account.Extra, "weekly", now); ok && utilization >= threshold7d {
			return true, openAIQuotaAutoPauseDecision{window: "weekly", threshold: threshold7d, utilization: utilization}
		}
	}
	return false, openAIQuotaAutoPauseDecision{}
}

func resolveCodingPlanQuotaUtilization(extra map[string]any, window string, now time.Time) (float64, bool) {
	var usedKey string
	switch window {
	case "5h":
		usedKey = "coding_plan_5h_used_percent"
	case "weekly":
		usedKey = "coding_plan_weekly_used_percent"
	default:
		return 0, false
	}
	usedPercent, ok := resolveAccountExtraNumber(extra, usedKey)
	if !ok || usedPercent <= 0 {
		return 0, false
	}
	if codingPlanQuotaWindowReset(extra, window, now) || codingPlanSnapshotStaleForPause(extra, now) {
		return 0, false
	}
	return clamp01(usedPercent / 100), true
}

func codingPlanSnapshotStaleForPause(extra map[string]any, now time.Time) bool {
	if len(extra) == 0 {
		return false
	}
	raw, ok := extra["coding_plan_usage_updated_at"]
	if !ok {
		return false
	}
	updatedAt, err := parseTime(fmt.Sprint(raw))
	if err != nil {
		return false
	}
	return now.Sub(updatedAt) >= codingPlanQuotaStaleAfter
}

func codingPlanQuotaWindowReset(extra map[string]any, window string, now time.Time) bool {
	if len(extra) == 0 {
		return false
	}
	prefix := "coding_plan_" + window + "_"
	if window == "weekly" {
		prefix = "coding_plan_weekly_"
	}
	if resetAtRaw, ok := extra[prefix+"reset_at"]; ok {
		if resetAt, err := parseTime(fmt.Sprint(resetAtRaw)); err == nil {
			return !now.Before(resetAt)
		}
	}
	resetAfter := parseExtraInt(extra[prefix+"reset_after_seconds"])
	if resetAfter <= 0 {
		return false
	}
	base := now
	if updatedRaw, ok := extra["coding_plan_usage_updated_at"]; ok {
		if updatedAt, err := parseTime(fmt.Sprint(updatedRaw)); err == nil {
			base = updatedAt
		}
	}
	return !now.Before(base.Add(time.Duration(resetAfter) * time.Second))
}

func parseCodingPlanResetTime(value any, now time.Time) *time.Time {
	if value == nil {
		return nil
	}
	switch v := value.(type) {
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil
		}
		if t, err := parseTime(s); err == nil {
			return &t
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return unixFlexibleTime(n)
		}
	case json.Number:
		if n, err := v.Int64(); err == nil {
			return unixFlexibleTime(n)
		}
	case float64:
		return unixFlexibleTime(int64(v))
	case int64:
		return unixFlexibleTime(v)
	case int:
		return unixFlexibleTime(int64(v))
	}
	return nil
}

func unixFlexibleTime(n int64) *time.Time {
	if n <= 0 {
		return nil
	}
	if n > 1_000_000_000_000 {
		t := time.UnixMilli(n).UTC()
		return &t
	}
	t := time.Unix(n, 0).UTC()
	return &t
}

func usedPercentFromLimitRemaining(limit, remaining float64) float64 {
	if limit <= 0 {
		return 0
	}
	used := limit - remaining
	if used < 0 {
		used = 0
	}
	return clampPercent(used / limit * 100)
}

func clampPercent(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

func remainingSeconds(resetAt time.Time, now time.Time) int {
	remaining := int(resetAt.Sub(now).Seconds())
	if remaining < 0 {
		return 0
	}
	return remaining
}

func firstNonEmptyCodingPlan(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func codingPlanStringPtr(value string) *string {
	return &value
}

func ClassifyCodingPlanProviderError(provider CodingPlanProvider, statusCode int, body []byte, account *Account) ProviderErrorDecision {
	now := time.Now()
	message := strings.ToLower(strings.TrimSpace(extractUpstreamErrorMessage(body)))
	if message == "" {
		message = strings.ToLower(strings.TrimSpace(string(body)))
	}
	decision := ProviderErrorDecision{}
	switch {
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		decision.AuthFailed = true
		decision.Reason = "credential_expired"
	case statusCode == http.StatusTooManyRequests:
		decision.RateLimited = true
		decision.Retryable = true
		decision.ShouldFailover = true
		decision.Reason = "rate_limited"
		until := codingPlanCooldownUntil(account, "5h", now, 5*time.Minute)
		decision.TempUnschedulableUntil = &until
	case statusCode == 529:
		decision.Overloaded = true
		decision.Retryable = true
		decision.ShouldFailover = true
		decision.Reason = "overloaded"
		until := now.Add(time.Minute)
		decision.TempUnschedulableUntil = &until
	case statusCode >= 500 && statusCode <= 599:
		decision.Overloaded = true
		decision.Retryable = true
		decision.ShouldFailover = true
		decision.Reason = "server_error"
		until := now.Add(time.Minute)
		decision.TempUnschedulableUntil = &until
	}

	if containsCodingPlanQuotaExhausted(message) {
		decision.QuotaExhausted = true
		decision.Retryable = true
		decision.ShouldFailover = true
		decision.Reason = "quota_exhausted"
		window := "5h"
		if strings.Contains(message, "week") || strings.Contains(message, "weekly") || strings.Contains(message, "7d") {
			window = "weekly"
		}
		until := codingPlanCooldownUntil(account, window, now, 30*time.Minute)
		decision.TempUnschedulableUntil = &until
	}

	if decision.Reason == "" {
		decision.Reason = fmt.Sprintf("%s_http_%d", provider, statusCode)
	}
	return decision
}

func containsCodingPlanQuotaExhausted(message string) bool {
	if message == "" {
		return false
	}
	keywords := []string{
		"quota exhausted",
		"insufficient quota",
		"limit exceeded",
		"usage limit",
		"rate limit exceeded",
		"afp exhausted",
		"余额不足",
		"配额不足",
		"额度不足",
		"配额已用尽",
		"额度已用尽",
	}
	for _, keyword := range keywords {
		if strings.Contains(message, keyword) {
			return true
		}
	}
	return false
}

func codingPlanCooldownUntil(account *Account, window string, now time.Time, fallback time.Duration) time.Time {
	if account != nil && account.Extra != nil {
		key := "coding_plan_5h_reset_at"
		if window == "weekly" {
			key = "coding_plan_weekly_reset_at"
		}
		if raw, ok := account.Extra[key]; ok {
			if resetAt, err := parseTime(fmt.Sprint(raw)); err == nil && resetAt.After(now) {
				return resetAt
			}
		}
	}
	return now.Add(fallback)
}
