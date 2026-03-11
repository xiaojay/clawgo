package clawgo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anthropics/clawgo/clawgo/schema"
	"gopkg.in/yaml.v3"
)

type Config struct {
	APIKey            string                       `yaml:"-"`
	Port              int64                        `yaml:"port"`
	Profile           string                       `yaml:"profile"`
	DebugHTTP         bool                         `yaml:"debug_http"`
	DebugTranscript   bool                         `yaml:"debug_transcript"`
	RequestTimeoutSec int64                        `yaml:"request_timeout_sec"`
	ModelsTimeoutSec  int64                        `yaml:"models_timeout_sec"`
	ConfigPath        string                       `yaml:"-"`
	Profiles          map[string]ProfileFileConfig `yaml:"profiles,omitempty"`
}

type ProfileFileConfig struct {
	Simple    []string `yaml:"simple"`
	Medium    []string `yaml:"medium"`
	Complex   []string `yaml:"complex"`
	Reasoning []string `yaml:"reasoning"`
}

func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	return &Config{
		Port:              8402,
		Profile:           "auto",
		RequestTimeoutSec: 45,
		ModelsTimeoutSec:  15,
		ConfigPath:        filepath.Join(home, ".clawgo", "config.yaml"),
	}
}

func LoadConfig() *Config {
	cfg := DefaultConfig()

	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" {
		cfg.APIKey = key
	}
	if port := os.Getenv("CLAWGO_PORT"); port != "" {
		if p, err := strconv.ParseInt(port, 10, 64); err == nil {
			cfg.Port = p
		}
	}
	if profile := os.Getenv("CLAWGO_PROFILE"); profile != "" {
		cfg.Profile = profile
	}
	if debugHTTP := os.Getenv("CLAWGO_DEBUG_HTTP"); debugHTTP != "" {
		if v, err := strconv.ParseBool(debugHTTP); err == nil {
			cfg.DebugHTTP = v
		}
	}
	if debugTranscript := os.Getenv("CLAWGO_DEBUG_TRANSCRIPT"); debugTranscript != "" {
		if v, err := strconv.ParseBool(debugTranscript); err == nil {
			cfg.DebugTranscript = v
		}
	}
	if timeout := os.Getenv("CLAWGO_REQUEST_TIMEOUT_SEC"); timeout != "" {
		if v, err := strconv.ParseInt(timeout, 10, 64); err == nil && v > 0 {
			cfg.RequestTimeoutSec = v
		}
	}
	if timeout := os.Getenv("CLAWGO_MODELS_TIMEOUT_SEC"); timeout != "" {
		if v, err := strconv.ParseInt(timeout, 10, 64); err == nil && v > 0 {
			cfg.ModelsTimeoutSec = v
		}
	}
	if configPath := os.Getenv("CLAWGO_CONFIG"); configPath != "" {
		cfg.ConfigPath = configPath
	}

	cfg.loadFile()
	return cfg
}

func (c *Config) loadFile() {
	data, err := os.ReadFile(c.ConfigPath)
	if err != nil {
		return
	}

	var fileCfg Config
	if err := yaml.Unmarshal(data, &fileCfg); err != nil {
		return
	}

	if c.Port == 8402 && fileCfg.Port != 0 {
		c.Port = fileCfg.Port
	}
	if c.Profile == "auto" && fileCfg.Profile != "" {
		c.Profile = fileCfg.Profile
	}
	if !c.DebugHTTP && fileCfg.DebugHTTP {
		c.DebugHTTP = true
	}
	if !c.DebugTranscript && fileCfg.DebugTranscript {
		c.DebugTranscript = true
	}
	if c.RequestTimeoutSec == 45 && fileCfg.RequestTimeoutSec > 0 {
		c.RequestTimeoutSec = fileCfg.RequestTimeoutSec
	}
	if c.ModelsTimeoutSec == 15 && fileCfg.ModelsTimeoutSec > 0 {
		c.ModelsTimeoutSec = fileCfg.ModelsTimeoutSec
	}
	if fileCfg.Profiles != nil {
		c.Profiles = fileCfg.Profiles
	}
}

func normalizeProfileName(name string) string {
	normalized := strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(normalized, "clawgo/") {
		normalized = strings.TrimPrefix(normalized, "clawgo/")
	}
	return normalized
}

func isBuiltInRoutingProfile(profile string) bool {
	switch normalizeProfileName(profile) {
	case "auto", "eco", "premium":
		return true
	default:
		return false
	}
}

func cloneTierConfigs(src map[schema.Tier]schema.TierConfig) map[schema.Tier]schema.TierConfig {
	cloned := make(map[schema.Tier]schema.TierConfig, len(src))
	for tier, cfg := range src {
		cloned[tier] = schema.TierConfig{
			Primary:  cfg.Primary,
			Fallback: append([]string(nil), cfg.Fallback...),
		}
	}
	return cloned
}

func tierConfigFromModels(models []string) schema.TierConfig {
	if len(models) == 0 {
		return schema.TierConfig{}
	}
	return schema.TierConfig{
		Primary:  models[0],
		Fallback: append([]string(nil), models[1:]...),
	}
}

func (p ProfileFileConfig) applyTo(base map[schema.Tier]schema.TierConfig) map[schema.Tier]schema.TierConfig {
	configs := cloneTierConfigs(base)
	if len(p.Simple) > 0 {
		configs[schema.TierSimple] = tierConfigFromModels(p.Simple)
	}
	if len(p.Medium) > 0 {
		configs[schema.TierMedium] = tierConfigFromModels(p.Medium)
	}
	if len(p.Complex) > 0 {
		configs[schema.TierComplex] = tierConfigFromModels(p.Complex)
	}
	if len(p.Reasoning) > 0 {
		configs[schema.TierReasoning] = tierConfigFromModels(p.Reasoning)
	}
	return configs
}

func (c *Config) lookupCustomProfile(profile string) (ProfileFileConfig, bool) {
	if c == nil || len(c.Profiles) == 0 {
		return ProfileFileConfig{}, false
	}

	normalized := normalizeProfileName(profile)
	for name, cfg := range c.Profiles {
		if normalizeProfileName(name) == normalized {
			return cfg, true
		}
	}
	return ProfileFileConfig{}, false
}

// ResolveRoutingProfile reports whether the given model name should trigger routing.
func (c *Config) ResolveRoutingProfile(model string) (string, bool) {
	profile := normalizeProfileName(model)
	if profile == "auto" {
		defaultProfile := normalizeProfileName(c.Profile)
		if defaultProfile == "" {
			defaultProfile = "auto"
		}
		return defaultProfile, true
	}
	if isBuiltInRoutingProfile(profile) {
		return profile, true
	}
	if _, ok := c.lookupCustomProfile(profile); ok {
		return profile, true
	}
	return "", false
}

// TierConfigs returns the effective tier mapping for a built-in or custom profile.
func (c *Config) TierConfigs(profile string) map[schema.Tier]schema.TierConfig {
	normalized := normalizeProfileName(profile)
	configs := cloneTierConfigs(DefaultTierConfigs(normalized))
	if custom, ok := c.lookupCustomProfile(normalized); ok {
		return custom.applyTo(configs)
	}
	return configs
}
