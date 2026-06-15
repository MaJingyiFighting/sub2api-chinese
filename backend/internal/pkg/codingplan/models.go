// Package codingplan declares static model catalogs for the domestic Coding
// Plan providers (Kimi / Zhipu / MiniMax / Volcengine / MiMo).
//
// These lists are used by the admin "available models" endpoint so that users
// configuring model_mapping for a Coding Plan account see the supplier's
// real upstream model names — not Claude's Anthropic models, which used to
// leak through as the fallback and pointed users in the wrong direction.
//
// The catalogs are intentionally conservative:
//   - Kimi/Zhipu/MiniMax: list models we have evidence of via either the
//     supplier's public Chat Completions docs or the in-app Coding Plan
//     model picker. Users can still override via model_mapping.
//   - Volcengine: lists the Ark Coding-tier doubao SKUs commonly used by
//     coding agents; until the official AgentPlan/AFP usage API is plumbed
//     in, this list is best-effort and the UI flags Volcengine quota probe
//     as experimental.
//   - MiMo: empty by design. The Coding Plan model catalog has not been
//     published publicly; users must add entries via model_mapping. The UI
//     marks MiMo as experimental in both the platform picker and the probe
//     status.
package codingplan

// Model mirrors openai.Model / claude.Model shape so the admin frontend can
// render a single dropdown regardless of platform.
type Model struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	DisplayName   string `json:"display_name"`
	RequestModel  string `json:"request_model,omitempty"`
	UpstreamModel string `json:"upstream_model,omitempty"`
	ModelRole     string `json:"model_role,omitempty"`
}

// KimiDefaultModels are the Moonshot/Kimi Coding Plan / Chat Completions
// models we surface as defaults. Sourced from Kimi's open-platform docs and
// the Coding Plan model picker.
var KimiDefaultModels = []Model{
	{ID: "kimi-k2-turbo-preview", Type: "model", DisplayName: "Kimi K2 Turbo Preview"},
	{ID: "kimi-latest", Type: "model", DisplayName: "Kimi Latest"},
	{ID: "moonshot-v1-128k", Type: "model", DisplayName: "Moonshot v1 128K"},
	{ID: "moonshot-v1-32k", Type: "model", DisplayName: "Moonshot v1 32K"},
	{ID: "moonshot-v1-8k", Type: "model", DisplayName: "Moonshot v1 8K"},
}

// ZhipuDefaultModels are the GLM/Zhipu Coding Plan models. Sourced from
// Zhipu's open.bigmodel.cn / api.z.ai docs.
var ZhipuDefaultModels = []Model{
	{ID: "glm-4.6", Type: "model", DisplayName: "GLM-4.6"},
	{ID: "glm-4.5", Type: "model", DisplayName: "GLM-4.5"},
	{ID: "glm-4-plus", Type: "model", DisplayName: "GLM-4-Plus"},
	{ID: "glm-4-air", Type: "model", DisplayName: "GLM-4-Air"},
	{ID: "glm-4-airx", Type: "model", DisplayName: "GLM-4-AirX"},
	{ID: "glm-4-long", Type: "model", DisplayName: "GLM-4-Long"},
	{ID: "glm-4-flash", Type: "model", DisplayName: "GLM-4-Flash"},
}

// MiniMaxDefaultModels are the MiniMax models commonly exposed via
// api.minimaxi.com / api.minimax.io.
var MiniMaxDefaultModels = []Model{
	{ID: "MiniMax-M3", Type: "model", DisplayName: "MiniMax-M3"},
	{ID: "MiniMax-Text-01", Type: "model", DisplayName: "MiniMax-Text-01"},
	{ID: "abab6.5s-chat", Type: "model", DisplayName: "abab6.5s-chat"},
	{ID: "abab6.5-chat", Type: "model", DisplayName: "abab6.5-chat"},
}

// VolcengineDefaultModels are the Ark Doubao models commonly used as the
// upstream target for Volcengine Coding/Agent Plans. The list is best-effort:
// Coding Plan plan-tier model availability varies per account, so users are
// expected to refine this via model_mapping.
var VolcengineDefaultModels = []Model{
	{ID: "doubao-seed-1.6", Type: "model", DisplayName: "Doubao Seed 1.6"},
	{ID: "doubao-seed-1.6-thinking", Type: "model", DisplayName: "Doubao Seed 1.6 Thinking"},
	{ID: "doubao-1.5-thinking-pro", Type: "model", DisplayName: "Doubao 1.5 Thinking Pro"},
	{ID: "doubao-1.5-pro-256k", Type: "model", DisplayName: "Doubao 1.5 Pro 256K"},
	{ID: "doubao-1.5-pro-32k", Type: "model", DisplayName: "Doubao 1.5 Pro 32K"},
	{ID: "doubao-pro-32k", Type: "model", DisplayName: "Doubao Pro 32K"},
}

// MiMoDefaultModels intentionally empty: no public catalog yet. Returned as
// an empty slice so the admin UI's mapping editor stays free-form for MiMo.
var MiMoDefaultModels = []Model{}

// DefaultModelsForProvider returns the default catalog for the given
// provider key. Unknown providers get an empty list, never a fallback to
// Claude/OpenAI catalogs (which would mis-suggest unsupported model names).
func DefaultModelsForProvider(provider string) []Model {
	switch provider {
	case "kimi":
		return KimiDefaultModels
	case "zhipu":
		return ZhipuDefaultModels
	case "minimax":
		return MiniMaxDefaultModels
	case "volcengine":
		return VolcengineDefaultModels
	case "mimo":
		return MiMoDefaultModels
	default:
		return []Model{}
	}
}
