package clawgo

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
)

const openRouterAuthURL = "https://openrouter.ai/api/v1/auth/key"

type BalanceMonitor struct {
	apiKey    string
	cacheTTL  time.Duration
	debugHTTP bool

	mu            sync.RWMutex
	cachedBalance float64
	cachedAt      time.Time
	hasCached     bool
}

func NewBalanceMonitor(apiKey string, cacheTTL time.Duration) *BalanceMonitor {
	if cacheTTL == 0 {
		cacheTTL = 30 * time.Second
	}
	return &BalanceMonitor{
		apiKey:   apiKey,
		cacheTTL: cacheTTL,
	}
}

// GetBalance returns available balance (limit - usage) from OpenRouter.
func (b *BalanceMonitor) GetBalance() (float64, error) {
	if bal, ok := b.getCachedBalance(); ok {
		return bal, nil
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", openRouterAuthURL, nil)
	if err != nil {
		return 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+b.apiKey)
	logDebugHTTPRequest(b.debugHTTP, "openrouter", req, nil)

	resp, err := client.Do(req)
	if err != nil {
		logDebugHTTPError(b.debugHTTP, "openrouter", req, err)
		return 0, fmt.Errorf("fetch balance: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("read balance: %w", err)
	}
	logDebugHTTPResponse(b.debugHTTP, "openrouter", resp, respBody)

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("fetch balance: status %d", resp.StatusCode)
	}

	var result schema.OpenRouterKeyResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return 0, fmt.Errorf("decode balance: %w", err)
	}

	balance := result.Data.Limit - result.Data.Usage
	if result.Data.Limit == 0 {
		// No limit set, treat as unlimited
		balance = 999999
	}

	b.setCachedBalance(balance)
	return balance, nil
}

// IsLowBalance returns true if available balance < $1.
func (b *BalanceMonitor) IsLowBalance() bool {
	bal, err := b.GetBalance()
	if err != nil {
		return false
	}
	return bal < 1.0
}

func (b *BalanceMonitor) getCachedBalance() (float64, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.hasCached {
		return 0, false
	}
	if time.Since(b.cachedAt) > b.cacheTTL {
		return 0, false
	}
	return b.cachedBalance, true
}

func (b *BalanceMonitor) setCachedBalance(balance float64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.cachedBalance = balance
	b.cachedAt = time.Now()
	b.hasCached = true
}
