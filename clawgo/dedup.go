package clawgo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"time"
)

const (
	defaultDedupTTL = 30 * time.Second
	maxBodySize     = 1048576 // 1MB
)

type CachedResponse struct {
	Status      int
	Headers     map[string]string
	Body        []byte
	CompletedAt time.Time
}

type inflightEntry struct {
	waiters []chan *CachedResponse
}

type Deduplicator struct {
	mu        sync.Mutex
	inflight  map[string]*inflightEntry
	completed map[string]*CachedResponse
	ttl       time.Duration
}

func NewDeduplicator(ttl time.Duration) *Deduplicator {
	if ttl == 0 {
		ttl = defaultDedupTTL
	}
	return &Deduplicator{
		inflight:  make(map[string]*inflightEntry),
		completed: make(map[string]*CachedResponse),
		ttl:       ttl,
	}
}

var timestampPattern = regexp.MustCompile(`^\[\w{3}\s+\d{4}-\d{2}-\d{2}\s+\d{2}:\d{2}\s+\w+\]\s*`)

// DedupHash creates a SHA-256 hash of a request body after canonicalization.
func DedupHash(body []byte) string {
	content := body
	var parsed interface{}
	if err := json.Unmarshal(body, &parsed); err == nil {
		stripped := stripTimestamps(parsed)
		canonical := canonicalize(stripped)
		if data, err := json.Marshal(canonical); err == nil {
			content = data
		}
	}
	h := sha256.Sum256(content)
	return fmt.Sprintf("%x", h[:8])
}

// GetCached returns a cached response if available and not expired.
func (d *Deduplicator) GetCached(key string) *CachedResponse {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry := d.completed[key]
	if entry == nil {
		return nil
	}
	if time.Since(entry.CompletedAt) > d.ttl {
		delete(d.completed, key)
		return nil
	}
	return entry
}

// GetInflight returns a channel that will receive the response when the in-flight request completes.
func (d *Deduplicator) GetInflight(key string) <-chan *CachedResponse {
	d.mu.Lock()
	defer d.mu.Unlock()
	entry := d.inflight[key]
	if entry == nil {
		return nil
	}
	ch := make(chan *CachedResponse, 1)
	entry.waiters = append(entry.waiters, ch)
	return ch
}

// MarkInflight marks a request as in-flight.
func (d *Deduplicator) MarkInflight(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inflight[key] = &inflightEntry{}
}

// Complete completes an in-flight request, caches result and notifies waiters.
func (d *Deduplicator) Complete(key string, result *CachedResponse) {
	d.mu.Lock()
	defer d.mu.Unlock()

	result.CompletedAt = time.Now()
	if len(result.Body) <= maxBodySize {
		d.completed[key] = result
	}

	if entry := d.inflight[key]; entry != nil {
		for _, ch := range entry.waiters {
			ch <- result
			close(ch)
		}
		delete(d.inflight, key)
	}

	d.prune()
}

// RemoveInflight removes an in-flight entry on error.
func (d *Deduplicator) RemoveInflight(key string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if entry := d.inflight[key]; entry != nil {
		errResp := &CachedResponse{
			Status:      503,
			Headers:     map[string]string{"content-type": "application/json"},
			Body:        []byte(`{"error":{"message":"Original request failed, please retry","type":"dedup_origin_failed"}}`),
			CompletedAt: time.Now(),
		}
		for _, ch := range entry.waiters {
			ch <- errResp
			close(ch)
		}
		delete(d.inflight, key)
	}
}

func (d *Deduplicator) prune() {
	now := time.Now()
	for key, entry := range d.completed {
		if now.Sub(entry.CompletedAt) > d.ttl {
			delete(d.completed, key)
		}
	}
}

// canonicalize sorts object keys recursively for consistent hashing.
func canonicalize(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		sorted := make(map[string]interface{}, len(val))
		for _, k := range keys {
			sorted[k] = canonicalize(val[k])
		}
		return sorted
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = canonicalize(item)
		}
		return result
	default:
		return v
	}
}

// stripTimestamps removes OpenClaw-injected timestamps from message content.
func stripTimestamps(v interface{}) interface{} {
	switch val := v.(type) {
	case map[string]interface{}:
		result := make(map[string]interface{}, len(val))
		for k, vv := range val {
			if k == "content" {
				if s, ok := vv.(string); ok {
					result[k] = timestampPattern.ReplaceAllString(s, "")
					continue
				}
			}
			result[k] = stripTimestamps(vv)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(val))
		for i, item := range val {
			result[i] = stripTimestamps(item)
		}
		return result
	default:
		return v
	}
}
