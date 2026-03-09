package clawgo

import (
	"testing"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
	"github.com/stretchr/testify/assert"
)

func TestSessionPinning(t *testing.T) {
	s := NewSessionStore(100 * time.Millisecond)
	defer s.Close()
	s.Set("sess1", "gpt-4", "MEDIUM")

	entry := s.Get("sess1")
	assert.NotNil(t, entry)
	assert.Equal(t, "gpt-4", entry.Model)
}

func TestSessionExpiry(t *testing.T) {
	s := NewSessionStore(100 * time.Millisecond)
	defer s.Close()
	s.Set("sess1", "gpt-4", "MEDIUM")

	time.Sleep(150 * time.Millisecond)
	entry := s.Get("sess1")
	assert.Nil(t, entry, "session should expire")
}

func TestDeriveSessionID(t *testing.T) {
	msgs := []schema.ChatMessage{
		{Role: "system", Content: "you are helpful"},
		{Role: "user", Content: "hello world"},
	}
	id1 := DeriveSessionID(msgs)
	id2 := DeriveSessionID(msgs)
	assert.Equal(t, id1, id2, "same messages should produce same session ID")
	assert.NotEmpty(t, id1)
}

func TestSessionThreeStrike(t *testing.T) {
	s := NewSessionStore(5 * time.Second)
	defer s.Close()
	s.Set("sess1", "gpt-4", "SIMPLE")

	assert.False(t, s.RecordRequestHash("sess1", "hash1"))
	assert.False(t, s.RecordRequestHash("sess1", "hash1"))
	assert.True(t, s.RecordRequestHash("sess1", "hash1"), "third consecutive same hash should trigger escalation")
}
