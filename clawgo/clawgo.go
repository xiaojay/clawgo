package clawgo

import (
	"fmt"
	"log"
	"time"
)

const Version = "0.1.0"

// ClawGo is the main application instance.
type ClawGo struct {
	config  *Config
	proxy   *Proxy
	catalog *ModelCatalog
	balance *BalanceMonitor
	router  *Router
	session *SessionStore
	dedup   *Deduplicator
	cache   *ResponseCache
}

// New creates a new ClawGo instance and initializes all components.
func New(cfg *Config) *ClawGo {
	if cfg.APIKey == "" {
		log.Fatal("OPENROUTER_API_KEY is required")
	}

	router := NewRouter()
	catalog := NewModelCatalog()
	balance := NewBalanceMonitor(cfg.APIKey, 30*time.Second)
	catalog.debugHTTP = cfg.DebugHTTP
	balance.debugHTTP = cfg.DebugHTTP
	session := NewSessionStore(30 * time.Minute)
	dedup := NewDeduplicator(30 * time.Second)
	cache := NewResponseCache(200, 10*time.Minute, 1048576)

	proxy := NewProxy(cfg, router, catalog, balance, session, dedup, cache)

	return &ClawGo{
		config:  cfg,
		proxy:   proxy,
		catalog: catalog,
		balance: balance,
		router:  router,
		session: session,
		dedup:   dedup,
		cache:   cache,
	}
}

// Run starts the ClawGo proxy server.
func (c *ClawGo) Run() error {
	ln, err := c.proxy.Listen()
	if err != nil {
		return err
	}

	// Fetch models from OpenRouter
	log.Printf("fetching models from OpenRouter...")
	if err := c.catalog.FetchModels(c.config.APIKey); err != nil {
		log.Printf("warning: failed to fetch models: %v (will retry on first request)", err)
	}

	// Check balance
	if bal, err := c.balance.GetBalance(); err == nil {
		log.Printf("OpenRouter balance: $%.2f", bal)
		if bal < 1.0 {
			log.Printf("warning: low balance (<$1.00)")
		}
	}

	fmt.Printf("\n  ClawGo v%s\n", Version)
	fmt.Printf("  Smart LLM Router → OpenRouter\n")
	fmt.Printf("  Profile: %s\n", c.config.Profile)
	fmt.Printf("  Debug HTTP: %t\n", c.config.DebugHTTP)
	fmt.Printf("  Models: %d loaded\n", c.catalog.Count())
	fmt.Printf("  Listening: http://localhost:%d\n\n", c.config.Port)

	return c.proxy.Serve(ln)
}

// Close gracefully shuts down all components.
func (c *ClawGo) Close() {
	c.session.Close()
	c.cache.Clear()
	log.Println("clawgo stopped")
}
