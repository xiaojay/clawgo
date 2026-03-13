package clawgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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

type concurrentWriteRecorder struct {
	header     http.Header
	body       bytes.Buffer
	statusCode int
	delay      time.Duration
	active     int32
	concurrent int32
	mu         sync.Mutex
}

func newConcurrentWriteRecorder(delay time.Duration) *concurrentWriteRecorder {
	return &concurrentWriteRecorder{
		header: make(http.Header),
		delay:  delay,
	}
}

func (w *concurrentWriteRecorder) Header() http.Header {
	return w.header
}

func (w *concurrentWriteRecorder) WriteHeader(statusCode int) {
	w.statusCode = statusCode
}

func (w *concurrentWriteRecorder) Write(p []byte) (int, error) {
	w.enterCriticalSection()
	defer w.leaveCriticalSection()

	w.mu.Lock()
	defer w.mu.Unlock()
	if w.statusCode == 0 {
		w.statusCode = http.StatusOK
	}
	return w.body.Write(p)
}

func (w *concurrentWriteRecorder) Flush() {
	w.enterCriticalSection()
	defer w.leaveCriticalSection()
}

func (w *concurrentWriteRecorder) enterCriticalSection() {
	if atomic.AddInt32(&w.active, 1) > 1 {
		atomic.StoreInt32(&w.concurrent, 1)
	}
	time.Sleep(w.delay)
}

func (w *concurrentWriteRecorder) leaveCriticalSection() {
	atomic.AddInt32(&w.active, -1)
}

func (w *concurrentWriteRecorder) BodyString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func (w *concurrentWriteRecorder) HadConcurrentWrite() bool {
	return atomic.LoadInt32(&w.concurrent) != 0
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

func TestEndToEndCustomProfileRoutingAndFallback(t *testing.T) {
	var attemptedModels []string

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			var req schema.ChatCompletionRequest
			err := json.NewDecoder(r.Body).Decode(&req)
			require.NoError(t, err)

			attemptedModels = append(attemptedModels, req.Model)
			if len(attemptedModels) == 1 {
				assert.Equal(t, "custom/primary", req.Model)
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"retry me"}`))
				return
			}

			assert.Equal(t, "custom/fallback", req.Model)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "chatcmpl-custom",
				"object": "chat.completion",
				"model":  req.Model,
				"choices": []map[string]interface{}{
					{
						"index":         0,
						"message":       map[string]string{"role": "assistant", "content": "4"},
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
	proxy.config.Profiles = map[string]ProfileFileConfig{
		"my-custom": {
			Simple:    []string{"custom/primary", "custom/fallback"},
			Medium:    []string{"custom/primary", "custom/fallback"},
			Complex:   []string{"custom/primary", "custom/fallback"},
			Reasoning: []string{"custom/primary", "custom/fallback"},
		},
	}

	chatReq := schema.ChatCompletionRequest{
		Model: "clawgo/my-custom",
		Messages: []schema.ChatMessage{
			{Role: "user", Content: "what is 2+2?"},
		},
	}
	bodyBytes, err := json.Marshal(chatReq)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, []string{"custom/primary", "custom/fallback"}, attemptedModels)

	var resp schema.ChatCompletionResponse
	err = json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Equal(t, "custom/fallback", resp.Model)
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

func TestEndToEndStreamingSerializesHeartbeatWrites(t *testing.T) {
	oldHeartbeatInterval := heartbeatInterval
	heartbeatInterval = 10 * time.Millisecond
	defer func() {
		heartbeatInterval = oldHeartbeatInterval
	}()

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)
		flusher.Flush()

		chunks := []string{
			`data: {"id":"chatcmpl-1","choices":[{"delta":{"role":"assistant"},"index":0}]}`,
			`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":"Hi"},"index":0}]}`,
			`data: {"id":"chatcmpl-1","choices":[{"delta":{"content":" there"},"index":0}]}`,
			`data: [DONE]`,
		}
		for i, chunk := range chunks {
			fmt.Fprintf(w, "%s\n\n", chunk)
			flusher.Flush()
			if i < len(chunks)-1 {
				time.Sleep(20 * time.Millisecond)
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
	bodyBytes, err := json.Marshal(chatReq)
	require.NoError(t, err)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	w := newConcurrentWriteRecorder(15 * time.Millisecond)
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.statusCode)
	assert.False(t, w.HadConcurrentWrite(), "stream and heartbeat must not write the response concurrently")
	assert.Contains(t, w.Header().Get("Content-Type"), "text/event-stream")
	assert.Contains(t, w.BodyString(), "data: [DONE]")
}

func TestDebugHTTPLogging(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	defer mock.Close()

	proxy, cleanup := newTestProxy("test-key", mock.URL)
	defer cleanup()
	proxy.config.DebugHTTP = true

	var logBuf bytes.Buffer
	oldWriter := log.Writer()
	log.SetOutput(&logBuf)
	defer log.SetOutput(oldWriter)

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
	assert.Contains(t, logBuf.String(), "debug_http inbound_request method=POST url=/v1/chat/completions")
	assert.Contains(t, logBuf.String(), "debug_http openrouter_request method=POST url="+mock.URL+"/v1/chat/completions")
	assert.Contains(t, logBuf.String(), `Authorization=["Bearer <redacted>"]`)
	assert.NotContains(t, logBuf.String(), "Bearer test-key")
}

func TestDebugTranscriptLoggingNonStreaming(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		cost := 0.000321
		upstreamCost := 0.000222
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
			"usage": map[string]interface{}{
				"prompt_tokens":     12,
				"completion_tokens": 3,
				"total_tokens":      15,
				"cost":              cost,
				"prompt_tokens_details": map[string]int64{
					"cached_tokens":      9,
					"cache_write_tokens": 18,
				},
				"completion_tokens_details": map[string]int64{
					"reasoning_tokens": 2,
				},
				"cost_details": map[string]float64{
					"upstream_inference_cost": upstreamCost,
				},
			},
		})
	}))
	defer mock.Close()

	proxy, cleanup := newTestProxy("test-key", mock.URL)
	defer cleanup()
	proxy.config.DebugTranscript = true

	var logBuf bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	chatReq := schema.ChatCompletionRequest{
		Model: "google/gemini-2.5-flash",
		Messages: []schema.ChatMessage{
			{Role: "system", Content: "you are helpful"},
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
	assert.Contains(t, logBuf.String(), "llm_transcript id=")
	assert.Contains(t, logBuf.String(), "source=upstream")
	assert.Contains(t, logBuf.String(), "[system]\nyou are helpful")
	assert.Contains(t, logBuf.String(), "[user]\nhello")
	assert.Contains(t, logBuf.String(), "[assistant]\nHello!")
	assert.Contains(t, logBuf.String(), "usage: prompt=12 completion=3 total=15")
	assert.Contains(t, logBuf.String(), "cache: hit=true cached_tokens=9 cache_write_tokens=18")
	assert.Contains(t, logBuf.String(), "reasoning: tokens=2")
	assert.Contains(t, logBuf.String(), "cost: total=$0.000321 upstream_inference=$0.000222")
}

func TestEndToEnd_AuthRequired(t *testing.T) {
	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()
	proxy.config.InternalSharedSecret = "test-secret"
	proxy.setupMux()

	body := []byte(`{"model":"auto","messages":[{"role":"user","content":"hi"}]}`)
	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestEndToEnd_AuthWithValidToken(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "chatcmpl-test", "object": "chat.completion",
			"model": "google/gemini-2.5-flash",
			"choices": []map[string]interface{}{
				{"index": 0, "message": map[string]string{"role": "assistant", "content": "Hi"}, "finish_reason": "stop"},
			},
		})
	}))
	defer mock.Close()

	proxy, cleanup := newTestProxy("test-key", mock.URL)
	defer cleanup()
	proxy.config.InternalSharedSecret = "test-secret"
	proxy.setupMux()

	token := generateTestInstanceToken(1, 1, "test-secret", time.Hour)
	chatReq := schema.ChatCompletionRequest{
		Model:    "google/gemini-2.5-flash",
		Messages: []schema.ChatMessage{{Role: "user", Content: "hello"}},
	}
	bodyBytes, _ := json.Marshal(chatReq)

	req := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEndToEnd_HealthNoAuthRequired(t *testing.T) {
	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()
	proxy.config.InternalSharedSecret = "test-secret"
	proxy.setupMux()

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestEndToEnd_SearchEndpoint(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"web": map[string]interface{}{
				"results": []map[string]interface{}{
					{"title": "Test", "url": "https://test.com", "description": "Test result"},
				},
			},
		})
	}))
	defer brave.Close()

	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()
	proxy.search = NewSearchHandler("brave-key", brave.URL)
	proxy.setupMux()

	body, _ := json.Marshal(SearchRequest{Query: "test"})
	req := httptest.NewRequest("POST", "/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp SearchResponse
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "Test", resp.Results[0].Title)
}

func TestEndToEnd_SearchWithAuthRequired(t *testing.T) {
	proxy, cleanup := newTestProxy("test-key", "")
	defer cleanup()
	proxy.config.InternalSharedSecret = "test-secret"
	proxy.search = NewSearchHandler("brave-key", "")
	proxy.setupMux()

	body, _ := json.Marshal(SearchRequest{Query: "test"})
	req := httptest.NewRequest("POST", "/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	proxy.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDebugTranscriptLoggingStreaming(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if ok {
			flusher.Flush()
		}
		chunks := []string{
			`data: {"id":"chatcmpl-1","model":"openai/gpt-4o","choices":[{"delta":{"role":"assistant"},"index":0}]}`,
			`data: {"id":"chatcmpl-1","model":"openai/gpt-4o","choices":[{"delta":{"content":"Hi"},"index":0}]}`,
			`data: {"id":"chatcmpl-1","model":"openai/gpt-4o","choices":[{"delta":{"content":" there"},"index":0,"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7,"cost":0.000111,"prompt_tokens_details":{"cached_tokens":5,"cache_write_tokens":0},"completion_tokens_details":{"reasoning_tokens":1}}}`,
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
	proxy.config.DebugTranscript = true

	var logBuf bytes.Buffer
	oldWriter := log.Writer()
	oldFlags := log.Flags()
	log.SetOutput(&logBuf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(oldWriter)
		log.SetFlags(oldFlags)
	}()

	chatReq := schema.ChatCompletionRequest{
		Model:  "openai/gpt-4o",
		Stream: true,
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
	assert.Contains(t, logBuf.String(), "stream=true")
	assert.Contains(t, logBuf.String(), "[assistant]\nHi there")
	assert.Contains(t, logBuf.String(), "usage: prompt=5 completion=2 total=7")
	assert.Contains(t, logBuf.String(), "cache: hit=true cached_tokens=5 cache_write_tokens=0")
	assert.Contains(t, logBuf.String(), "reasoning: tokens=1")
	assert.Contains(t, logBuf.String(), "cost: total=$0.000111")
	assert.NotContains(t, logBuf.String(), "stream_chunk")
}
