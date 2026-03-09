package clawgo

import (
	"testing"

	"github.com/anthropics/clawgo/clawgo/schema"
	"github.com/stretchr/testify/assert"
)

func TestSelectModel(t *testing.T) {
	catalog := NewModelCatalog()
	tierConfigs := DefaultTierConfigs("auto")

	decision := SelectModel(schema.TierSimple, 0.9, "rules", "test",
		tierConfigs, catalog, 1000, 500, "auto", 0)

	assert.NotEmpty(t, decision.Model)
	assert.Equal(t, schema.TierSimple, decision.Tier)
	assert.Equal(t, 0.9, decision.Confidence)
}

func TestGetFallbackChain(t *testing.T) {
	tierConfigs := DefaultTierConfigs("auto")
	chain := GetFallbackChain(schema.TierMedium, tierConfigs)
	assert.True(t, len(chain) >= 2, "should have primary + at least 1 fallback")
	assert.Equal(t, tierConfigs[schema.TierMedium].Primary, chain[0])
}

func TestCalculateModelCost(t *testing.T) {
	catalog := NewModelCatalog()
	cost := CalculateModelCost("test-model", catalog, 1000, 500, "auto")
	// With empty catalog, costs should be 0
	assert.Equal(t, 0.0, cost.CostEstimate)
}

func TestSelectModelSavings(t *testing.T) {
	catalog := NewModelCatalog()
	tierConfigs := DefaultTierConfigs("premium")

	decision := SelectModel(schema.TierComplex, 0.95, "rules", "test",
		tierConfigs, catalog, 1000, 500, "premium", 0)

	// Premium profile should have 0 savings
	assert.Equal(t, 0.0, decision.Savings)
}
