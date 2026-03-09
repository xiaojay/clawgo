package clawgo

import (
	"testing"

	"github.com/anthropics/clawgo/clawgo/schema"
	"github.com/stretchr/testify/assert"
)

func TestClassifySimple(t *testing.T) {
	r := NewRouter()
	result := r.Classify("hello, how are you?", "", 10)
	assert.NotNil(t, result.Tier)
	assert.Equal(t, schema.TierSimple, *result.Tier)
}

func TestClassifyReasoning(t *testing.T) {
	r := NewRouter()
	result := r.Classify("prove this theorem step by step using mathematical proof", "", 100)
	assert.NotNil(t, result.Tier)
	assert.Equal(t, schema.TierReasoning, *result.Tier)
}

func TestClassifyCode(t *testing.T) {
	r := NewRouter()
	result := r.Classify("implement a function that uses async await with import and class", "", 200)
	assert.NotNil(t, result.Tier)
	tier := *result.Tier
	assert.True(t, tier == schema.TierMedium || tier == schema.TierComplex || tier == schema.TierReasoning)
}

func TestClassifyAgentic(t *testing.T) {
	r := NewRouter()
	result := r.Classify("read file, edit the code, fix the bug, then deploy and verify", "", 100)
	assert.True(t, result.AgenticScore >= 0.5, "should detect agentic task, got %f", result.AgenticScore)
}

func TestSigmoidConfidence(t *testing.T) {
	c := calibrateConfidence(0.5, 12)
	assert.True(t, c > 0.9, "high distance should give high confidence")

	c = calibrateConfidence(0, 12)
	assert.InDelta(t, 0.5, c, 0.01, "zero distance should give ~0.5 confidence")
}
