package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestValidateCodingPlanProvider_DomesticPlatformMatchesProvider(t *testing.T) {
	err := validateCodingPlanProvider("kimi", service.AccountTypeAPIKey, nil, map[string]any{
		"coding_plan_provider": "kimi",
	})
	require.NoError(t, err)

	err = validateCodingPlanProvider("zhipu", service.AccountTypeUpstream, nil, map[string]any{
		"coding_plan_provider": " zhipu ",
	})
	require.NoError(t, err)
}

func TestValidateCodingPlanProvider_RejectsOpenAIPlatformDisguise(t *testing.T) {
	err := validateCodingPlanProvider(service.PlatformOpenAI, service.AccountTypeAPIKey, nil, map[string]any{
		"coding_plan_provider": "kimi",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "domestic platforms")
}

func TestValidateCodingPlanProvider_RejectsMismatchedProvider(t *testing.T) {
	err := validateCodingPlanProvider("kimi", service.AccountTypeAPIKey, nil, map[string]any{
		"coding_plan_provider": "zhipu",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be the same")
}

func TestValidateCodingPlanProvider_RejectsOpenAIWithDomesticBaseURL(t *testing.T) {
	err := validateCodingPlanProvider(service.PlatformOpenAI, service.AccountTypeAPIKey, map[string]any{
		"base_url": "https://api.moonshot.cn/v1",
	}, map[string]any{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "platform=kimi")
}

func TestAccountHandlerGetAvailableModels_CodingPlanUsesProviderModels(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       46,
			Name:     "kimi-coding-plan",
			Platform: "kimi",
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Extra: map[string]any{
				"coding_plan_provider": "kimi",
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/46/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID            string `json:"id"`
			UpstreamModel string `json:"upstream_model"`
			ModelRole     string `json:"model_role"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.Data)
	require.Equal(t, "kimi-k2-turbo-preview", resp.Data[0].ID)
	require.Equal(t, "kimi-k2-turbo-preview", resp.Data[0].UpstreamModel)
	require.Equal(t, "upstream", resp.Data[0].ModelRole)
	for _, model := range resp.Data {
		require.NotContains(t, model.ID, "claude")
	}
}

func TestAccountHandlerGetAvailableModels_CodingPlanMappingKeysOverrideCatalog(t *testing.T) {
	svc := &availableModelsAdminService{
		stubAdminService: newStubAdminService(),
		account: service.Account{
			ID:       47,
			Name:     "zhipu-coding-plan",
			Platform: "zhipu",
			Type:     service.AccountTypeAPIKey,
			Status:   service.StatusActive,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.5": "glm-4.6",
				},
			},
			Extra: map[string]any{
				"coding_plan_provider": "zhipu",
			},
		},
	}
	router := setupAvailableModelsRouter(svc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/47/models", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Data []struct {
			ID            string `json:"id"`
			RequestModel  string `json:"request_model"`
			UpstreamModel string `json:"upstream_model"`
			ModelRole     string `json:"model_role"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Data, 1)
	require.Equal(t, "gpt-5.5", resp.Data[0].ID)
	require.Equal(t, "gpt-5.5", resp.Data[0].RequestModel)
	require.Equal(t, "glm-4.6", resp.Data[0].UpstreamModel)
	require.Equal(t, "request", resp.Data[0].ModelRole)
}
