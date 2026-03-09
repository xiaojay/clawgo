package clawgo

import (
	"github.com/anthropics/clawgo/clawgo/schema"
)

const (
	baselineModelID     = "anthropic/claude-opus-4"
	baselineInputPrice  = 5.0  // per 1M tokens
	baselineOutputPrice = 25.0 // per 1M tokens
)

// CostResult holds cost calculation results.
type CostResult struct {
	CostEstimate float64
	BaselineCost float64
	Savings      float64
}

// SelectModel selects the primary model for a tier and builds RoutingDecision.
func SelectModel(
	tier schema.Tier,
	confidence float64,
	method string,
	reasoning string,
	tierConfigs map[schema.Tier]schema.TierConfig,
	catalog *ModelCatalog,
	estimatedInputTokens int64,
	maxOutputTokens int64,
	profile string,
	agenticScore float64,
) schema.RoutingDecision {
	config := tierConfigs[tier]
	model := config.Primary

	inputPrice, outputPrice := catalog.GetPricing(model)
	inputCost := float64(estimatedInputTokens) / 1_000_000 * inputPrice
	outputCost := float64(maxOutputTokens) / 1_000_000 * outputPrice
	costEstimate := inputCost + outputCost

	// Baseline cost (Claude Opus pricing)
	bInputPrice, bOutputPrice := catalog.GetPricing(baselineModelID)
	if bInputPrice == 0 {
		bInputPrice = baselineInputPrice
	}
	if bOutputPrice == 0 {
		bOutputPrice = baselineOutputPrice
	}
	baselineInput := float64(estimatedInputTokens) / 1_000_000 * bInputPrice
	baselineOutput := float64(maxOutputTokens) / 1_000_000 * bOutputPrice
	baselineCost := baselineInput + baselineOutput

	savings := 0.0
	if profile != "premium" && baselineCost > 0 {
		savings = (baselineCost - costEstimate) / baselineCost
		if savings < 0 {
			savings = 0
		}
	}

	return schema.RoutingDecision{
		Model:        model,
		Tier:         tier,
		Confidence:   confidence,
		Method:       method,
		Reasoning:    reasoning,
		CostEstimate: costEstimate,
		BaselineCost: baselineCost,
		Savings:      savings,
		AgenticScore: agenticScore,
	}
}

// GetFallbackChain returns ordered fallback chain: [primary, ...fallbacks].
func GetFallbackChain(tier schema.Tier, tierConfigs map[schema.Tier]schema.TierConfig) []string {
	config := tierConfigs[tier]
	chain := make([]string, 0, 1+len(config.Fallback))
	chain = append(chain, config.Primary)
	chain = append(chain, config.Fallback...)
	return chain
}

// CalculateModelCost calculates cost for a specific model (used on fallback).
func CalculateModelCost(
	model string,
	catalog *ModelCatalog,
	estimatedInputTokens int64,
	maxOutputTokens int64,
	profile string,
) CostResult {
	inputPrice, outputPrice := catalog.GetPricing(model)
	inputCost := float64(estimatedInputTokens) / 1_000_000 * inputPrice
	outputCost := float64(maxOutputTokens) / 1_000_000 * outputPrice
	costEstimate := inputCost + outputCost

	bInputPrice, bOutputPrice := catalog.GetPricing(baselineModelID)
	if bInputPrice == 0 {
		bInputPrice = baselineInputPrice
	}
	if bOutputPrice == 0 {
		bOutputPrice = baselineOutputPrice
	}
	baselineCost := float64(estimatedInputTokens)/1_000_000*bInputPrice +
		float64(maxOutputTokens)/1_000_000*bOutputPrice

	savings := 0.0
	if profile != "premium" && baselineCost > 0 {
		savings = (baselineCost - costEstimate) / baselineCost
		if savings < 0 {
			savings = 0
		}
	}

	return CostResult{
		CostEstimate: costEstimate,
		BaselineCost: baselineCost,
		Savings:      savings,
	}
}

// FilterByToolCalling filters model list to those supporting tools.
func FilterByToolCalling(models []string, hasTools bool, catalog *ModelCatalog) []string {
	if !hasTools {
		return models
	}
	var filtered []string
	for _, m := range models {
		info := catalog.GetModel(m)
		if info != nil && info.SupportsTools {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return models // fallback to full list
	}
	return filtered
}

// FilterByVision filters model list to those supporting vision.
func FilterByVision(models []string, hasVision bool, catalog *ModelCatalog) []string {
	if !hasVision {
		return models
	}
	var filtered []string
	for _, m := range models {
		info := catalog.GetModel(m)
		if info != nil && info.SupportsVision {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return models
	}
	return filtered
}
