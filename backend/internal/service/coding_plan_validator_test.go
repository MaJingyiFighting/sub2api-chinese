//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// ---- ValidateCodingPlanAccountConsistency (Task B3) ----

func TestValidateCodingPlanAccountConsistency_Rules(t *testing.T) {
	t.Run("openai + domestic base_url rejected", func(t *testing.T) {
		err := ValidateCodingPlanAccountConsistency(PlatformOpenAI, AccountTypeAPIKey,
			map[string]any{"base_url": "https://api.moonshot.cn/v1"}, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "Coding Plan")
	})

	t.Run("openai + coding_plan_provider rejected", func(t *testing.T) {
		err := ValidateCodingPlanAccountConsistency(PlatformOpenAI, AccountTypeAPIKey,
			nil, map[string]any{"coding_plan_provider": "kimi"})
		require.Error(t, err)
	})

	t.Run("provider mismatch rejected", func(t *testing.T) {
		err := ValidateCodingPlanAccountConsistency(string(CodingPlanProviderKimi), AccountTypeAPIKey,
			nil, map[string]any{"coding_plan_provider": "zhipu"})
		require.Error(t, err)
		require.Contains(t, err.Error(), "does not match platform")
	})

	t.Run("provider + openai_chat wire_api allowed", func(t *testing.T) {
		err := ValidateCodingPlanAccountConsistency(string(CodingPlanProviderKimi), AccountTypeAPIKey,
			map[string]any{"base_url": "https://api.moonshot.cn/v1", "wire_api": "openai_chat"},
			map[string]any{"coding_plan_provider": "kimi"})
		require.NoError(t, err)
	})

	t.Run("provider + anthropic_messages wire_api allowed", func(t *testing.T) {
		err := ValidateCodingPlanAccountConsistency(string(CodingPlanProviderKimi), AccountTypeAPIKey,
			map[string]any{"base_url": "https://api.moonshot.cn/anthropic", "wire_api": "anthropic_messages"},
			map[string]any{"coding_plan_provider": "kimi"})
		require.NoError(t, err)
	})

	t.Run("no coding_plan_provider is unconstrained", func(t *testing.T) {
		require.NoError(t, ValidateCodingPlanAccountConsistency(PlatformOpenAI, AccountTypeAPIKey,
			map[string]any{"base_url": "https://api.openai.com"}, nil))
	})
}

// ---- Update / BulkUpdate merged validation (Task B3 bypass fix) ----

func TestUpdateAccount_RejectsDomesticBaseUrlMergedOntoOpenAI(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		getByIDAccounts: map[int64]*Account{
			5: {
				ID:          5,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.openai.com", "api_key": "sk-openai"},
				Extra:       map[string]any{},
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	// Partial update that only changes base_url to a domestic Coding Plan URL.
	// The merged state (platform=openai + domestic base_url) must be rejected,
	// proving the partial-update bypass is closed.
	_, err := svc.UpdateAccount(context.Background(), 5, &UpdateAccountInput{
		Credentials: map[string]any{"base_url": "https://api.moonshot.cn/v1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Coding Plan")
	require.Empty(t, repo.bulkUpdateIDs, "update must be rejected before any write")
}

func TestBulkUpdateAccounts_RejectsDomesticBaseUrlMergedOntoOpenAIByIDs(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		getByIDsAccounts: []*Account{
			{
				ID:          7,
				Platform:    PlatformOpenAI,
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.openai.com"},
				Extra:       map[string]any{},
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	// Bulk-update by account_ids must not bypass coding-plan validation: injecting
	// a Zhipu base_url onto an openai account is rejected on the merged state.
	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs:  []int64{7},
		Credentials: map[string]any{"base_url": "https://open.bigmodel.cn/api/coding/paas/v4"},
	})
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "account 7")
	require.Empty(t, repo.bulkUpdateIDs, "bulk update must be rejected before any write")
}

func TestBulkUpdateAccounts_RejectsMismatchedProviderViaExtraByIDs(t *testing.T) {
	repo := &accountRepoStubForBulkUpdate{
		getByIDsAccounts: []*Account{
			{
				ID:          8,
				Platform:    string(CodingPlanProviderKimi),
				Type:        AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://api.moonshot.cn/v1"},
				Extra:       map[string]any{"coding_plan_provider": "kimi"},
			},
		},
	}
	svc := &adminServiceImpl{accountRepo: repo}

	result, err := svc.BulkUpdateAccounts(context.Background(), &BulkUpdateAccountsInput{
		AccountIDs: []int64{8},
		Extra:      map[string]any{"coding_plan_provider": "zhipu"},
	})
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "does not match platform")
}

// ---- Group binding (Task B2) ----

func newDomesticVariantAccount(provider, wireAPI string) *Account {
	return &Account{
		ID:       1,
		Platform: provider,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-x",
			"base_url": "https://api.moonshot.cn/v1",
			"wire_api": wireAPI,
		},
		Extra: map[string]any{"coding_plan_provider": provider},
	}
}

func TestValidateAccountGroupBinding_AnthropicVariantJoinsAnthropicGroup(t *testing.T) {
	svc := &adminServiceImpl{
		groupRepo: &groupRepoStubForAdmin{
			getByID: &Group{ID: 20, Name: "anthropic-default", Platform: PlatformAnthropic},
		},
	}
	// Anthropic variant of a domestic provider MAY join an Anthropic group.
	account := newDomesticVariantAccount(string(CodingPlanProviderKimi), "anthropic_messages")
	require.NoError(t, svc.validateAccountGroupBinding(context.Background(), account, []int64{20}))
}

func TestValidateAccountGroupBinding_ChatVariantRejectedFromAnthropicGroup(t *testing.T) {
	svc := &adminServiceImpl{
		groupRepo: &groupRepoStubForAdmin{
			getByID: &Group{ID: 20, Name: "anthropic-default", Platform: PlatformAnthropic},
		},
	}
	// The Chat/Codex variant is NOT an Anthropic Messages account, so it stays
	// barred from Anthropic groups.
	account := newDomesticVariantAccount(string(CodingPlanProviderKimi), "openai_chat")
	require.Error(t, svc.validateAccountGroupBinding(context.Background(), account, []int64{20}))
}

func TestValidateAccountGroupBinding_ChatVariantRejectedFromOpenAIGroup(t *testing.T) {
	svc := &adminServiceImpl{
		groupRepo: &groupRepoStubForAdmin{
			getByID: &Group{ID: 10, Name: "openai-default", Platform: PlatformOpenAI},
		},
	}
	account := newDomesticVariantAccount(string(CodingPlanProviderKimi), "openai_chat")
	err := svc.validateAccountGroupBinding(context.Background(), account, []int64{10})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot bind to group")
}

func TestValidateAccountGroupBinding_ProviderIsolation(t *testing.T) {
	svc := &adminServiceImpl{
		groupRepo: &groupRepoStubForAdmin{
			getByID: &Group{ID: 30, Name: "zhipu-default", Platform: string(CodingPlanProviderZhipu)},
		},
	}
	// A kimi account cannot join a zhipu group (domestic provider isolation),
	// even as the anthropic variant.
	account := newDomesticVariantAccount(string(CodingPlanProviderKimi), "anthropic_messages")
	require.Error(t, svc.validateAccountGroupBinding(context.Background(), account, []int64{30}))
}

func TestValidateAccountGroupBinding_SameProviderGroupAllowed(t *testing.T) {
	svc := &adminServiceImpl{
		groupRepo: &groupRepoStubForAdmin{
			getByID: &Group{ID: 40, Name: "kimi-default", Platform: string(CodingPlanProviderKimi)},
		},
	}
	account := newDomesticVariantAccount(string(CodingPlanProviderKimi), "openai_chat")
	require.NoError(t, svc.validateAccountGroupBinding(context.Background(), account, []int64{40}))
}
