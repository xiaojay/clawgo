package schema

type Tier string

const (
	TierSimple    Tier = "SIMPLE"
	TierMedium    Tier = "MEDIUM"
	TierComplex   Tier = "COMPLEX"
	TierReasoning Tier = "REASONING"
)

type DimensionScore struct {
	Name   string
	Score  float64
	Signal string
}

type ScoringResult struct {
	Score        float64
	Tier         *Tier
	Confidence   float64
	Signals      []string
	AgenticScore float64
	Dimensions   []DimensionScore
}

type RoutingDecision struct {
	Model        string  `json:"model"`
	Tier         Tier    `json:"tier"`
	Confidence   float64 `json:"confidence"`
	Method       string  `json:"method"`
	Reasoning    string  `json:"reasoning"`
	CostEstimate float64 `json:"cost_estimate"`
	BaselineCost float64 `json:"baseline_cost"`
	Savings      float64 `json:"savings"`
	AgenticScore float64 `json:"agentic_score,omitempty"`
}

type TierConfig struct {
	Primary  string   `yaml:"primary" json:"primary"`
	Fallback []string `yaml:"fallback" json:"fallback"`
}

type ProfileConfig struct {
	Simple    TierConfig `yaml:"simple" json:"simple"`
	Medium    TierConfig `yaml:"medium" json:"medium"`
	Complex   TierConfig `yaml:"complex" json:"complex"`
	Reasoning TierConfig `yaml:"reasoning" json:"reasoning"`
}

type TierBoundaries struct {
	SimpleMedium     float64 `yaml:"simple_medium"`
	MediumComplex    float64 `yaml:"medium_complex"`
	ComplexReasoning float64 `yaml:"complex_reasoning"`
}
