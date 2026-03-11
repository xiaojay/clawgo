package clawgo

import (
	"os"
	"testing"

	"github.com/anthropics/clawgo/clawgo/schema"
	"github.com/stretchr/testify/assert"
)

func TestConfigDefaults(t *testing.T) {
	cfg := DefaultConfig()
	assert.Equal(t, int64(8402), cfg.Port)
	assert.Equal(t, "auto", cfg.Profile)
	assert.NotEmpty(t, cfg.ConfigPath)
}

func TestConfigFromEnv(t *testing.T) {
	os.Setenv("OPENROUTER_API_KEY", "test-key-123")
	os.Setenv("CLAWGO_PORT", "9999")
	os.Setenv("CLAWGO_PROFILE", "eco")
	os.Setenv("CLAWGO_DEBUG_HTTP", "true")
	os.Setenv("CLAWGO_DEBUG_TRANSCRIPT", "true")
	defer func() {
		os.Unsetenv("OPENROUTER_API_KEY")
		os.Unsetenv("CLAWGO_PORT")
		os.Unsetenv("CLAWGO_PROFILE")
		os.Unsetenv("CLAWGO_DEBUG_HTTP")
		os.Unsetenv("CLAWGO_DEBUG_TRANSCRIPT")
	}()

	cfg := LoadConfig()
	assert.Equal(t, "test-key-123", cfg.APIKey)
	assert.Equal(t, int64(9999), cfg.Port)
	assert.Equal(t, "eco", cfg.Profile)
	assert.True(t, cfg.DebugHTTP)
	assert.True(t, cfg.DebugTranscript)
}

func TestResolveRoutingProfileUsesConfiguredDefault(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Profile = "my-custom"
	cfg.Profiles = map[string]ProfileFileConfig{
		"my-custom": {
			Simple:    []string{"custom/simple"},
			Medium:    []string{"custom/medium"},
			Complex:   []string{"custom/complex"},
			Reasoning: []string{"custom/reasoning"},
		},
	}

	profile, ok := cfg.ResolveRoutingProfile("auto")
	assert.True(t, ok)
	assert.Equal(t, "my-custom", profile)

	profile, ok = cfg.ResolveRoutingProfile("clawgo/my-custom")
	assert.True(t, ok)
	assert.Equal(t, "my-custom", profile)

	profile, ok = cfg.ResolveRoutingProfile("premium")
	assert.True(t, ok)
	assert.Equal(t, "premium", profile)

	_, ok = cfg.ResolveRoutingProfile("openai/gpt-4o")
	assert.False(t, ok)
}

func TestTierConfigsUsesCustomProfiles(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Profiles = map[string]ProfileFileConfig{
		"auto": {
			Simple: []string{"custom/simple-primary", "custom/simple-fallback"},
		},
		"my-custom": {
			Simple:    []string{"custom/simple-primary", "custom/simple-fallback"},
			Medium:    []string{"custom/medium-primary"},
			Complex:   []string{"custom/complex-primary", "custom/complex-fallback-1", "custom/complex-fallback-2"},
			Reasoning: []string{"custom/reasoning-primary"},
		},
	}

	autoConfigs := cfg.TierConfigs("auto")
	assert.Equal(t, "custom/simple-primary", autoConfigs[schema.TierSimple].Primary)
	assert.Equal(t, []string{"custom/simple-fallback"}, autoConfigs[schema.TierSimple].Fallback)
	assert.NotEmpty(t, autoConfigs[schema.TierMedium].Primary)

	customConfigs := cfg.TierConfigs("my-custom")
	assert.Equal(t, "custom/medium-primary", customConfigs[schema.TierMedium].Primary)
	assert.Empty(t, customConfigs[schema.TierMedium].Fallback)
	assert.Equal(t, "custom/complex-primary", customConfigs[schema.TierComplex].Primary)
	assert.Equal(t, []string{"custom/complex-fallback-1", "custom/complex-fallback-2"}, customConfigs[schema.TierComplex].Fallback)
}
