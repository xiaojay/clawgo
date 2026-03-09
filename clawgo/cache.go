package clawgo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type CachedLLMResponse struct {
	Body      []byte
	Status    int
	Headers   map[string]string
	Model     string
	CachedAt  time.Time
	ExpiresAt time.Time
}

type ResponseCache struct {
	mu          sync.RWMutex
	cache       map[string]*CachedLLMResponse
	order       []string // insertion order for LRU
	maxSize     int
	defaultTTL  time.Duration
	maxItemSize int

	hits      int64
	misses    int64
	evictions int64
}

func NewResponseCache(maxSize int, defaultTTL time.Duration, maxItemSize int) *ResponseCache {
	if maxSize == 0 {
		maxSize = 200
	}
	if defaultTTL == 0 {
		defaultTTL = 10 * time.Minute
	}
	if maxItemSize == 0 {
		maxItemSize = 1048576 // 1MB
	}
	return &ResponseCache{
		cache:       make(map[string]*CachedLLMResponse),
		order:       make([]string, 0),
		maxSize:     maxSize,
		defaultTTL:  defaultTTL,
		maxItemSize: maxItemSize,
	}
}

// ResponseCacheKey generates a cache key from request body.
func ResponseCacheKey(body []byte) string {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err == nil {
		// Strip fields that don't affect response
		delete(parsed, "stream")
		delete(parsed, "user")
		delete(parsed, "request_id")
		delete(parsed, "x-request-id")
		canonical := canonicalize(parsed)
		if data, err := json.Marshal(canonical); err == nil {
			h := sha256.Sum256(data)
			return fmt.Sprintf("%x", h[:16])
		}
	}
	h := sha256.Sum256(body)
	return fmt.Sprintf("%x", h[:16])
}

// Get returns a cached response if available and not expired.
func (c *ResponseCache) Get(key string) *CachedLLMResponse {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry := c.cache[key]
	if entry == nil {
		c.mu.RUnlock()
		c.mu.Lock()
		c.misses++
		c.mu.Unlock()
		c.mu.RLock()
		return nil
	}

	if time.Now().After(entry.ExpiresAt) {
		c.mu.RUnlock()
		c.mu.Lock()
		delete(c.cache, key)
		c.misses++
		c.mu.Unlock()
		c.mu.RLock()
		return nil
	}

	c.mu.RUnlock()
	c.mu.Lock()
	c.hits++
	c.mu.Unlock()
	c.mu.RLock()
	return entry
}

// Set caches a response.
func (c *ResponseCache) Set(key string, response *CachedLLMResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(response.Body) > c.maxItemSize {
		return
	}
	if response.Status >= 400 {
		return
	}

	// Evict if at capacity
	for len(c.cache) >= c.maxSize && len(c.order) > 0 {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.cache, oldest)
		c.evictions++
	}

	now := time.Now()
	response.CachedAt = now
	if response.ExpiresAt.IsZero() {
		response.ExpiresAt = now.Add(c.defaultTTL)
	}

	c.cache[key] = response
	c.order = append(c.order, key)
}

// Clear removes all entries.
func (c *ResponseCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = make(map[string]*CachedLLMResponse)
	c.order = c.order[:0]
}

// Stats returns cache statistics.
func (c *ResponseCache) Stats() (size int, hits, misses, evictions int64) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache), c.hits, c.misses, c.evictions
}
