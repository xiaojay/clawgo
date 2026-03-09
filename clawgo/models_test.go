package clawgo

import (
	"testing"

	"github.com/anthropics/clawgo/clawgo/schema"
	"github.com/stretchr/testify/assert"
)

func TestDefaultTierConfigs(t *testing.T) {
	configs := DefaultTierConfigs("auto")
	assert.NotEmpty(t, configs[schema.TierSimple].Primary)
	assert.NotEmpty(t, configs[schema.TierMedium].Primary)
	assert.NotEmpty(t, configs[schema.TierComplex].Primary)
	assert.NotEmpty(t, configs[schema.TierReasoning].Primary)
}

func TestDefaultTierConfigsEco(t *testing.T) {
	configs := DefaultTierConfigs("eco")
	assert.NotEmpty(t, configs[schema.TierSimple].Primary)
}

func TestDefaultTierConfigsPremium(t *testing.T) {
	configs := DefaultTierConfigs("premium")
	assert.Contains(t, configs[schema.TierComplex].Primary, "claude")
}

func TestAutoClassifyByPrice(t *testing.T) {
	c := NewModelCatalog()
	tier := c.ClassifyByPrice(0.1, 0.4)
	assert.Equal(t, schema.TierSimple, tier)

	tier = c.ClassifyByPrice(1.5, 10.0)
	assert.Equal(t, schema.TierMedium, tier)

	tier = c.ClassifyByPrice(5.0, 25.0)
	assert.Equal(t, schema.TierComplex, tier)

	tier = c.ClassifyByPrice(15.0, 60.0)
	assert.Equal(t, schema.TierReasoning, tier)
}

func TestParseTokenPrice(t *testing.T) {
	// OpenRouter returns price per token as string
	// $1 per 1M tokens = 0.000001 per token
	p := parseTokenPrice("0.000001")
	assert.InDelta(t, 1.0, p, 0.001)

	p = parseTokenPrice("0.000005")
	assert.InDelta(t, 5.0, p, 0.001)

	p = parseTokenPrice("0")
	assert.Equal(t, 0.0, p)

	p = parseTokenPrice("invalid")
	assert.Equal(t, 0.0, p)
}
