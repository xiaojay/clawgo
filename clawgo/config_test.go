package clawgo

import (
	"os"
	"testing"

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
