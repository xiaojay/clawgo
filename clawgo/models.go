package clawgo

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
)

const openRouterModelsURL = "https://openrouter.ai/api/v1/models"

// ModelCatalog holds all available models fetched from OpenRouter.
type ModelCatalog struct {
	mu     sync.RWMutex
	models map[string]*schema.ModelInfo
}

func NewModelCatalog() *ModelCatalog {
	return &ModelCatalog{
		models: make(map[string]*schema.ModelInfo),
	}
}

// FetchModels fetches model list from OpenRouter API.
func (c *ModelCatalog) FetchModels(apiKey string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequest("GET", openRouterModelsURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch models: status %d", resp.StatusCode)
	}

	var result schema.OpenRouterModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("decode models: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.models = make(map[string]*schema.ModelInfo, len(result.Data))
	for _, m := range result.Data {
		info := &schema.ModelInfo{
			ID:            m.ID,
			Name:          m.Name,
			Pricing:       m.Pricing,
			ContextLength: m.ContextLength,
			TopProvider:   m.TopProvider,
		}
		// Detect capabilities from architecture
		if m.Architecture != nil {
			if m.Architecture.Modality == "text+image->text" {
				info.SupportsVision = true
			}
		}
		c.models[m.ID] = info
	}

	log.Printf("models loaded: count=%d", len(c.models))
	return nil
}

// GetModel returns a model by ID.
func (c *ModelCatalog) GetModel(id string) *schema.ModelInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.models[id]
}

// GetPricing returns input/output price per 1M tokens for a model.
func (c *ModelCatalog) GetPricing(modelID string) (inputPrice, outputPrice float64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	m := c.models[modelID]
	if m == nil {
		return 0, 0
	}
	return parseTokenPrice(m.Pricing.Prompt), parseTokenPrice(m.Pricing.Completion)
}

// Count returns the number of loaded models.
func (c *ModelCatalog) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.models)
}

// ClassifyByPrice auto-classifies a model into a tier based on pricing.
// Used as fallback for models not in default tier configs.
func (c *ModelCatalog) ClassifyByPrice(inputPricePerM, outputPricePerM float64) schema.Tier {
	avg := (inputPricePerM + outputPricePerM) / 2
	switch {
	case avg < 1.0:
		return schema.TierSimple
	case avg < 8.0:
		return schema.TierMedium
	case avg < 20.0:
		return schema.TierComplex
	default:
		return schema.TierReasoning
	}
}

// parseTokenPrice converts OpenRouter's per-token string price to per-1M-tokens float.
func parseTokenPrice(s string) float64 {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v * 1_000_000
}

// DefaultTierConfigs returns the default tier->model mapping for a routing profile.
func DefaultTierConfigs(profile string) map[schema.Tier]schema.TierConfig {
	switch profile {
	case "eco":
		return map[schema.Tier]schema.TierConfig{
			schema.TierSimple: {
				Primary:  "google/gemini-2.5-flash-lite",
				Fallback: []string{"deepseek/deepseek-chat"},
			},
			schema.TierMedium: {
				Primary:  "google/gemini-2.5-flash-lite",
				Fallback: []string{"deepseek/deepseek-chat"},
			},
			schema.TierComplex: {
				Primary:  "google/gemini-2.5-flash",
				Fallback: []string{"deepseek/deepseek-chat"},
			},
			schema.TierReasoning: {
				Primary:  "deepseek/deepseek-r1",
				Fallback: []string{"google/gemini-2.5-flash"},
			},
		}
	case "premium":
		return map[schema.Tier]schema.TierConfig{
			schema.TierSimple: {
				Primary:  "anthropic/claude-haiku",
				Fallback: []string{"google/gemini-2.5-flash"},
			},
			schema.TierMedium: {
				Primary:  "anthropic/claude-sonnet-4",
				Fallback: []string{"google/gemini-2.5-pro", "openai/gpt-4o"},
			},
			schema.TierComplex: {
				Primary:  "anthropic/claude-opus-4",
				Fallback: []string{"openai/gpt-4o", "anthropic/claude-sonnet-4"},
			},
			schema.TierReasoning: {
				Primary:  "openai/o3-pro",
				Fallback: []string{"anthropic/claude-sonnet-4", "openai/o3"},
			},
		}
	default: // "auto"
		return map[schema.Tier]schema.TierConfig{
			schema.TierSimple: {
				Primary:  "google/gemini-2.5-flash-lite",
				Fallback: []string{"deepseek/deepseek-chat"},
			},
			schema.TierMedium: {
				Primary:  "google/gemini-2.5-flash",
				Fallback: []string{"deepseek/deepseek-chat", "google/gemini-2.5-flash-lite"},
			},
			schema.TierComplex: {
				Primary:  "google/gemini-2.5-pro",
				Fallback: []string{"google/gemini-2.5-flash", "deepseek/deepseek-chat"},
			},
			schema.TierReasoning: {
				Primary:  "anthropic/claude-sonnet-4",
				Fallback: []string{"deepseek/deepseek-r1", "openai/o3"},
			},
		}
	}
}
