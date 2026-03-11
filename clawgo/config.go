package clawgo

import (
	"os"
	"path/filepath"
	"strconv"

	"gopkg.in/yaml.v3"
)

type Config struct {
	APIKey            string                       `yaml:"-"`
	Port              int64                        `yaml:"port"`
	Profile           string                       `yaml:"profile"`
	DebugHTTP         bool                         `yaml:"debug_http"`
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
