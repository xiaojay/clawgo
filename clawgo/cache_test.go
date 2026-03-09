package clawgo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestResponseCacheBasic(t *testing.T) {
	c := NewResponseCache(10, 1*time.Second, 1024*1024)
	c.Set("k1", &CachedLLMResponse{Body: []byte("response"), Status: 200, Model: "gpt-4"})

	got := c.Get("k1")
	assert.NotNil(t, got)
	assert.Equal(t, "gpt-4", got.Model)
}

func TestResponseCacheExpiry(t *testing.T) {
	c := NewResponseCache(10, 100*time.Millisecond, 1024*1024)
	c.Set("k1", &CachedLLMResponse{Body: []byte("response"), Status: 200, Model: "gpt-4"})

	time.Sleep(150 * time.Millisecond)
	got := c.Get("k1")
	assert.Nil(t, got, "expired entry should not be returned")
}

func TestResponseCacheLRUEviction(t *testing.T) {
	c := NewResponseCache(2, 10*time.Second, 1024*1024)
	c.Set("k1", &CachedLLMResponse{Body: []byte("1"), Status: 200, Model: "m1"})
	c.Set("k2", &CachedLLMResponse{Body: []byte("2"), Status: 200, Model: "m2"})
	c.Set("k3", &CachedLLMResponse{Body: []byte("3"), Status: 200, Model: "m3"})

	assert.Nil(t, c.Get("k1"), "oldest entry should be evicted")
	assert.NotNil(t, c.Get("k3"))
}

func TestResponseCacheSkipsErrors(t *testing.T) {
	c := NewResponseCache(10, 10*time.Second, 1024*1024)
	c.Set("k1", &CachedLLMResponse{Body: []byte("error"), Status: 500, Model: "gpt-4"})
	assert.Nil(t, c.Get("k1"), "error responses should not be cached")
}

func TestResponseCacheKey(t *testing.T) {
	// Same content different field order should produce same key
	k1 := ResponseCacheKey([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`))
	k2 := ResponseCacheKey([]byte(`{"messages":[{"role":"user","content":"hi"}],"model":"gpt-4"}`))
	assert.Equal(t, k1, k2)

	// stream field should be ignored
	k3 := ResponseCacheKey([]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	assert.Equal(t, k1, k3)
}
