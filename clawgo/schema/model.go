package schema

type ModelInfo struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	Pricing        ModelPricing `json:"pricing"`
	ContextLength  int64        `json:"context_length"`
	TopProvider    *TopProvider `json:"top_provider,omitempty"`
	SupportsTools  bool         `json:"-"`
	SupportsVision bool         `json:"-"`
}

type ModelPricing struct {
	Prompt     string `json:"prompt"`
	Completion string `json:"completion"`
}

type TopProvider struct {
	ContextLength   int64 `json:"context_length,omitempty"`
	MaxOutputTokens int64 `json:"max_output_tokens,omitempty"`
	IsModerated     bool  `json:"is_moderated,omitempty"`
}

type ModelCatalog struct {
	Models map[string]*ModelInfo
}

type OpenRouterModelsResponse struct {
	Data []OpenRouterModel `json:"data"`
}

type OpenRouterModel struct {
	ID            string             `json:"id"`
	Name          string             `json:"name"`
	Pricing       ModelPricing       `json:"pricing"`
	ContextLength int64              `json:"context_length"`
	TopProvider   *TopProvider       `json:"top_provider,omitempty"`
	Architecture  *ModelArchitecture `json:"architecture,omitempty"`
}

type ModelArchitecture struct {
	Modality     string `json:"modality,omitempty"`
	Tokenizer    string `json:"tokenizer,omitempty"`
	InstructType string `json:"instruct_type,omitempty"`
}

type OpenRouterKeyResponse struct {
	Data OpenRouterKeyData `json:"data"`
}

type OpenRouterKeyData struct {
	Label      string     `json:"label"`
	Usage      float64    `json:"usage"`
	Limit      float64    `json:"limit"`
	IsFreeTier bool       `json:"is_free_tier"`
	RateLimit  *RateLimit `json:"rate_limit,omitempty"`
}

type RateLimit struct {
	Requests int64  `json:"requests"`
	Interval string `json:"interval"`
}
