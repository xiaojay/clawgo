package clawgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestProxy creates a Proxy wired with defaults suitable for testing.
// If mockURL is non-empty, the proxy's baseURL is set to point at a mock server.
func newTestProxy(apiKey string, mockURL string) (*Proxy, func()) {
	cfg := DefaultConfig()
	cfg.APIKey = apiKey

	router := NewRouter()
	catalog := NewModelCatalog()
	balance := NewBalanceMonitor(apiKey, 30*time.Second)
	balance.setCachedBalance(50.0) // pre-cache to avoid real API call
	session := NewSessionStore(30 * time.Minute)
	dedup := NewDeduplicator(30 * time.Second)
	cache := NewResponseCache(200, 10*time.Minute, 1048576)

	proxy := NewProxy(cfg, router, catalog, balance, session, dedup, cache)
	if mockURL != "" {
		proxy.baseURL = mockURL
	}

	cleanup := func() {
		session.Close()
	}
	return proxy, cleanup
}

func TestHealthEndpoint(t *testing.T) {
	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var health schema.HealthResponse
	err := json.Unmarshal(w.Body.Bytes(), &health)
	require.NoError(t, err)
	assert.Equal(t, "ok", health.Status)
	assert.Equal(t, "0.1.0", health.Version)
	assert.InDelta(t, 50.0, health.Balance, 0.01)
}

func TestRoutingDecision(t *testing.T) {
	router := NewRouter()

	// Simple request
	result := router.Classify("what is 2+2?", "", 10)
	assert.NotNil(t, result.Tier)
	assert.Equal(t, schema.TierSimple, *result.Tier)

	// Complex request
	result = router.Classify("design a distributed microservice architecture with kubernetes", "", 200)
	assert.NotNil(t, result.Tier)
	tier := *result.Tier
	assert.True(t, tier == schema.TierMedium || tier == schema.TierComplex || tier == schema.TierReasoning,
		"technical request should be at least MEDIUM, got %s", tier)
}

func TestIsRoutingProfile(t *testing.T) {
	assert.True(t, isRoutingProfile("auto"))
	assert.True(t, isRoutingProfile("eco"))
	assert.True(t, isRoutingProfile("premium"))
	assert.True(t, isRoutingProfile("clawgo/auto"))
	assert.False(t, isRoutingProfile("gpt-4"))
	assert.False(t, isRoutingProfile("anthropic/claude-sonnet-4"))
}

func TestRewriteModel(t *testing.T) {
	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hello"}]}`)
	rewritten := rewriteModel(body, "google/gemini-2.5-flash")

	var parsed map[string]interface{}
	err := json.Unmarshal(rewritten, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "google/gemini-2.5-flash", parsed["model"])

	// Messages should be preserved
	msgs, ok := parsed["messages"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, msgs, 1)
}

func TestDedupIntegration(t *testing.T) {
	dedup := NewDeduplicator(5 * time.Second)

	body := []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`)
	key := DedupHash(body)

	// Mark in-flight
	dedup.MarkInflight(key)

	// Another request with same body should get inflight channel
	ch := dedup.GetInflight(key)
	assert.NotNil(t, ch)

	// Complete the original
	go func() {
		dedup.Complete(key, &CachedResponse{Status: 200, Body: []byte(`{"result":"ok"}`)})
	}()

	// Waiter should get the result
	result := <-ch
	assert.Equal(t, 200, result.Status)

	// Subsequent request should get cached
	cached := dedup.GetCached(key)
	assert.NotNil(t, cached)
	assert.Equal(t, 200, cached.Status)
}

func TestEndToEndNonStreaming(t *testing.T) {
	// Mock OpenRouter server
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			// Verify auth header is forwarded
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer test-key")

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "chatcmpl-test",
				"object": "chat.completion",
				"model":  "google/gemini-2.5-flash",
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"message":       map[string]string{"role": "assistant", "content": "Hello!"},
						"finish_reason": "stop",
					},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()

	proxy, cleanup := newTestProxy("test-key", mock.URL)
	defer cleanup()

	// Send a chat completion request with a specific model (non-routing)
	chatReq := schema.ChatCompletionRequest{
		Model: "google/gemini-2.5-flash",
		Messages: []schema.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}
	bodyBytes, err := json.Marshal(chatReq)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp schema.ChatCompletionResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "chatcmpl-test", resp.ID)
	assert.Len(t, resp.Choices, 1)
}

func TestEndToEndMethodNotAllowed(t *testing.T) {
	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestListenReturnsHelpfulPortConflictError(t *testing.T) {
	occupied, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	defer occupied.Close()

	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()
	proxy.config.Port = int64(occupied.Addr().(*net.TCPAddr).Port)

	_, err = proxy.Listen()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already in use")
	assert.Contains(t, err.Error(), "--port")
	assert.Contains(t, err.Error(), "CLAWGO_PORT")
}

func TestEndToEndInvalidJSON(t *testing.T) {
	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var errResp schema.ErrorResponse
	err := json.Unmarshal(w.Body.Bytes(), &errResp)
	require.NoError(t, err)
	assert.Equal(t, "invalid JSON", errResp.Error.Message)
}

func TestEndToEndModelsProxy(t *testing.T) {
	// Mock models endpoint
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			assert.Contains(t, r.Header.Get("Authorization"), "Bearer test-key")
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{
					{"id": "openai/gpt-4", "name": "GPT-4"},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mock.Close()

	proxy, cleanup := newTestProxy("test-key", mock.URL)
	defer cleanup()

	req := httptest.NewRequest("GET", "/v1/models", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	data, ok := resp["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, data, 1)
}

func TestEndToEndUpstreamError(t *testing.T) {
	// Mock that returns 500
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal"}`))
	}))
	defer mock.Close()

	proxy, cleanup := newTestProxy("test-key", mock.URL)
	defer cleanup()

	chatReq := schema.ChatCompletionRequest{
		Model: "openai/gpt-4",
		Messages: []schema.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(chatReq)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	// Should return 502 after all fallback attempts fail
	assert.Equal(t, http.StatusBadGateway, w.Code)
}

func TestEndToEndStreaming(t *testing.T) {
	// Mock streaming endpoint
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}
		// Send SSE chunks
		chunks := []string{
			`data: {"id":"chatcmpl-1","choices":[{"delta":{"role":"assistant"},"index":0}]}`,
			`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"Hi"},"index":0}]}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "%s\n\n", chunk)
			if ok {
				flusher.Flush()
			}
		}
	}))
	defer mock.Close()

	proxy, cleanup := newTestProxy("test-key", mock.URL)
	defer cleanup()

	chatReq := schema.ChatCompletionRequest{
		Model:  "openai/gpt-4",
		Stream: true,
		Messages: []schema.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}
	bodyBytes, _ := json.Marshal(chatReq)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	assert.Contains(t, w.Body.String(), "data: [DONE]")
}
