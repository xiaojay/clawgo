package clawgo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDedupHash(t *testing.T) {
	h1 := DedupHash([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`))
	h2 := DedupHash([]byte(`{"messages":[{"role":"user","content":"hello"}],"model":"gpt-4"}`))
	assert.Equal(t, h1, h2, "canonicalized JSON should produce same hash")
}

func TestDedupHashDifferent(t *testing.T) {
	h1 := DedupHash([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`))
	h2 := DedupHash([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"world"}]}`))
	assert.NotEqual(t, h1, h2, "different content should produce different hash")
}

func TestDedupCacheExpiry(t *testing.T) {
	d := NewDeduplicator(100 * time.Millisecond)
	d.Complete("key1", &CachedResponse{Status: 200, Body: []byte("ok")})
	cached := d.GetCached("key1")
	assert.NotNil(t, cached)

	time.Sleep(150 * time.Millisecond)
	cached = d.GetCached("key1")
	assert.Nil(t, cached, "should expire after TTL")
}

func TestDedupInflight(t *testing.T) {
	d := NewDeduplicator(5 * time.Second)
	d.MarkInflight("key1")

	// Get waiter channel
	ch := d.GetInflight("key1")
	assert.NotNil(t, ch)

	// Complete in background
	go func() {
		time.Sleep(50 * time.Millisecond)
		d.Complete("key1", &CachedResponse{Status: 200, Body: []byte("done")})
	}()

	// Wait for result
	result := <-ch
	assert.Equal(t, 200, result.Status)
	assert.Equal(t, "done", string(result.Body))
}

func TestDedupTimestampStripping(t *testing.T) {
	h1 := DedupHash([]byte(`{"messages":[{"role":"user","content":"[SUN 2026-02-07 13:30 PST] hello"}]}`))
	h2 := DedupHash([]byte(`{"messages":[{"role":"user","content":"[MON 2026-02-08 14:00 PST] hello"}]}`))
	assert.Equal(t, h1, h2, "timestamps should be stripped before hashing")
}
