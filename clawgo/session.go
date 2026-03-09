package clawgo

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
)

const defaultSessionTimeout = 30 * time.Minute

type SessionEntry struct {
	Model        string
	Tier         string
	CreatedAt    time.Time
	LastUsedAt   time.Time
	RequestCount int64
	// Three-strike escalation
	RecentHashes []string
	Strikes      int
	Escalated    bool
}

type SessionStore struct {
	mu       sync.RWMutex
	sessions map[string]*SessionEntry
	timeout  time.Duration
	stopCh   chan struct{}
}

func NewSessionStore(timeout time.Duration) *SessionStore {
	if timeout == 0 {
		timeout = defaultSessionTimeout
	}
	s := &SessionStore{
		sessions: make(map[string]*SessionEntry),
		timeout:  timeout,
		stopCh:   make(chan struct{}),
	}
	go s.cleanupLoop()
	return s
}

// Get returns the pinned model for a session, if any.
func (s *SessionStore) Get(sessionID string) *SessionEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	entry := s.sessions[sessionID]
	if entry == nil {
		return nil
	}
	if time.Since(entry.LastUsedAt) > s.timeout {
		return nil
	}
	return entry
}

// Set pins a model to a session.
func (s *SessionStore) Set(sessionID, model, tier string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if existing := s.sessions[sessionID]; existing != nil {
		existing.LastUsedAt = now
		existing.RequestCount++
		if existing.Model != model {
			existing.Model = model
			existing.Tier = tier
		}
	} else {
		s.sessions[sessionID] = &SessionEntry{
			Model:        model,
			Tier:         tier,
			CreatedAt:    now,
			LastUsedAt:   now,
			RequestCount: 1,
		}
	}
}

// Touch extends a session's timeout.
func (s *SessionStore) Touch(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.sessions[sessionID]; entry != nil {
		entry.LastUsedAt = time.Now()
		entry.RequestCount++
	}
}

// RecordRequestHash records content hash and detects repetitive patterns.
// Returns true if 3+ consecutive similar requests (three-strike escalation).
func (s *SessionStore) RecordRequestHash(sessionID, hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.sessions[sessionID]
	if entry == nil {
		return false
	}

	if len(entry.RecentHashes) > 0 && entry.RecentHashes[len(entry.RecentHashes)-1] == hash {
		entry.Strikes++
	} else {
		entry.Strikes = 0
	}

	entry.RecentHashes = append(entry.RecentHashes, hash)
	if len(entry.RecentHashes) > 3 {
		entry.RecentHashes = entry.RecentHashes[1:]
	}

	return entry.Strikes >= 2 && !entry.Escalated
}

// EscalateSession bumps session to next tier. Returns new model/tier or nil.
func (s *SessionStore) EscalateSession(sessionID string, tierConfigs map[schema.Tier]schema.TierConfig) (string, string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.sessions[sessionID]
	if entry == nil {
		return "", "", false
	}

	tierOrder := []string{"SIMPLE", "MEDIUM", "COMPLEX", "REASONING"}
	currentIdx := -1
	for i, t := range tierOrder {
		if t == entry.Tier {
			currentIdx = i
			break
		}
	}
	if currentIdx < 0 || currentIdx >= len(tierOrder)-1 {
		return "", "", false
	}

	nextTier := schema.Tier(tierOrder[currentIdx+1])
	config := tierConfigs[nextTier]

	entry.Model = config.Primary
	entry.Tier = string(nextTier)
	entry.Strikes = 0
	entry.Escalated = true

	return config.Primary, string(nextTier), true
}

// Close stops the cleanup goroutine.
func (s *SessionStore) Close() {
	close(s.stopCh)
}

func (s *SessionStore) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.cleanup()
		case <-s.stopCh:
			return
		}
	}
}

func (s *SessionStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for id, entry := range s.sessions {
		if now.Sub(entry.LastUsedAt) > s.timeout {
			delete(s.sessions, id)
		}
	}
}

// DeriveSessionID derives a stable session ID from the first user message.
func DeriveSessionID(messages []schema.ChatMessage) string {
	for _, m := range messages {
		if m.Role == "user" {
			var content string
			switch v := m.Content.(type) {
			case string:
				content = v
			default:
				data, _ := json.Marshal(v)
				content = string(data)
			}
			h := sha256.Sum256([]byte(content))
			return fmt.Sprintf("%x", h[:4])
		}
	}
	return ""
}

// GetSessionIDFromHeader extracts session ID from request headers.
func GetSessionIDFromHeader(headers map[string]string) string {
	if id := headers["x-session-id"]; id != "" {
		return id
	}
	if id := headers["X-Session-ID"]; id != "" {
		return id
	}
	return ""
}
