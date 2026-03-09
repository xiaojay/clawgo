package clawgo

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBalanceCache(t *testing.T) {
	b := NewBalanceMonitor("fake-key", 1*time.Second)
	b.setCachedBalance(42.5)
	bal, ok := b.getCachedBalance()
	assert.True(t, ok)
	assert.InDelta(t, 42.5, bal, 0.01)

	// Wait for cache expiry
	time.Sleep(1100 * time.Millisecond)
	_, ok = b.getCachedBalance()
	assert.False(t, ok)
}

func TestBalanceMonitorDefaults(t *testing.T) {
	b := NewBalanceMonitor("test-key", 0)
	assert.Equal(t, 30*time.Second, b.cacheTTL)
}
