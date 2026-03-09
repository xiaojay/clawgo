package clawgo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
)

const (
	openRouterChatURL         = "https://openrouter.ai/api/v1/chat/completions"
	openRouterModelsURLProxy  = "https://openrouter.ai/api/v1/models"
	heartbeatInterval         = 2 * time.Second
	maxFallbackAttempts       = 5
	requestTimeout            = 180 * time.Second
)

// Proxy is the HTTP proxy server.
type Proxy struct {
	config  *Config
	router  *Router
	catalog *ModelCatalog
	balance *BalanceMonitor
	session *SessionStore
	dedup   *Deduplicator
	cache   *ResponseCache
	mux     *http.ServeMux
	client  *http.Client
	baseURL string // override for testing; empty uses production URLs
}

// ServeHTTP implements http.Handler by delegating to the internal mux.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mux.ServeHTTP(w, r)
}

// NewProxy creates a new Proxy with all dependencies wired.
func NewProxy(cfg *Config, router *Router, catalog *ModelCatalog, balance *BalanceMonitor, session *SessionStore, dedup *Deduplicator, cache *ResponseCache) *Proxy {
	p := &Proxy{
		config:  cfg,
		router:  router,
		catalog: catalog,
		balance: balance,
		session: session,
		dedup:   dedup,
		cache:   cache,
		mux:     http.NewServeMux(),
		client:  &http.Client{Timeout: requestTimeout},
	}

	p.mux.HandleFunc("/v1/chat/completions", p.handleChatCompletions)
	p.mux.HandleFunc("/health", p.handleHealth)
	p.mux.HandleFunc("/v1/models", p.handleModels)

	return p
}

// Run starts the proxy on the configured port.
func (p *Proxy) Run() error {
	addr := fmt.Sprintf(":%d", p.config.Port)
	log.Printf("proxy started addr=%s profile=%s models=%d", addr, p.config.Profile, p.catalog.Count())
	return http.ListenAndServe(addr, p.mux)
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	bal, _ := p.balance.GetBalance()
	resp := schema.HealthResponse{
		Status:  "ok",
		Version: "0.1.0",
		Port:    p.config.Port,
		Profile: p.config.Profile,
		Balance: bal,
		Models:  int64(p.catalog.Count()),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	modelsURL := openRouterModelsURLProxy
	if p.baseURL != "" {
		modelsURL = p.baseURL + "/v1/models"
	}
	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: err.Error(), Type: "proxy_error"},
		})
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)

	resp, err := p.client.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: err.Error(), Type: "upstream_error"},
		})
		return
	}
	defer resp.Body.Close()

	for k, v := range resp.Header {
		if len(v) > 0 {
			w.Header().Set(k, v[0])
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

func (p *Proxy) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: "method not allowed", Type: "invalid_request"},
		})
		return
	}

	// 1. Read request body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: "failed to read request body", Type: "invalid_request"},
		})
		return
	}

	var chatReq schema.ChatCompletionRequest
	if err := json.Unmarshal(bodyBytes, &chatReq); err != nil {
		writeJSON(w, http.StatusBadRequest, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: "invalid JSON", Type: "invalid_request"},
		})
		return
	}

	// 2. Dedup check
	dedupKey := DedupHash(bodyBytes)
	if cached := p.dedup.GetCached(dedupKey); cached != nil {
		log.Printf("dedup hit key=%s", dedupKey)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cached.Status)
		w.Write(cached.Body)
		return
	}
	if ch := p.dedup.GetInflight(dedupKey); ch != nil {
		log.Printf("dedup waiting key=%s", dedupKey)
		result := <-ch
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(result.Status)
		w.Write(result.Body)
		return
	}
	p.dedup.MarkInflight(dedupKey)

	// 3. Resolve model
	model := chatReq.Model
	isAutoRoute := isRoutingProfile(model)

	var decision *schema.RoutingDecision
	if isAutoRoute {
		decision = p.routeRequest(&chatReq, model)
		model = decision.Model
	}

	// 4. Session pinning
	sessionID := GetSessionIDFromHeader(extractHeaders(r))
	if sessionID == "" {
		sessionID = DeriveSessionID(chatReq.Messages)
	}
	if sessionID != "" && isAutoRoute {
		if entry := p.session.Get(sessionID); entry != nil {
			model = entry.Model
			log.Printf("session pinned session=%s model=%s", sessionID, model)
		}
	}

	// 5. Response cache check (non-streaming only)
	if !chatReq.Stream {
		cacheKey := ResponseCacheKey(bodyBytes)
		if cached := p.cache.Get(cacheKey); cached != nil {
			log.Printf("cache hit model=%s", cached.Model)
			p.dedup.Complete(dedupKey, &CachedResponse{Status: cached.Status, Body: cached.Body})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(cached.Status)
			w.Write(cached.Body)
			return
		}
	}

	// 6. Get fallback chain
	tierConfigs := DefaultTierConfigs(p.config.Profile)
	var fallbackChain []string
	if decision != nil {
		fallbackChain = GetFallbackChain(decision.Tier, tierConfigs)
	} else {
		fallbackChain = []string{model}
	}

	// 7. Forward with fallback
	var lastErr error
	for i, tryModel := range fallbackChain {
		if i >= maxFallbackAttempts {
			break
		}

		if chatReq.Stream {
			err = p.forwardStreaming(w, bodyBytes, tryModel, dedupKey)
		} else {
			err = p.forwardNonStreaming(w, bodyBytes, tryModel, dedupKey)
		}

		if err == nil {
			// Update session
			if sessionID != "" && isAutoRoute {
				tier := "MEDIUM"
				if decision != nil {
					tier = string(decision.Tier)
				}
				p.session.Set(sessionID, tryModel, tier)
			}
			if decision != nil {
				log.Printf("routed tier=%s model=%s confidence=%.2f savings=%.0f%%",
					decision.Tier, tryModel, decision.Confidence, decision.Savings*100)
			}
			return
		}

		log.Printf("fallback model=%s err=%v next=%d", tryModel, err, i+1)
		lastErr = err
	}

	// All attempts failed
	p.dedup.RemoveInflight(dedupKey)
	writeJSON(w, http.StatusBadGateway, schema.ErrorResponse{
		Error: schema.ErrorDetail{
			Message: fmt.Sprintf("all models failed: %v", lastErr),
			Type:    "upstream_error",
		},
	})
}

func (p *Proxy) routeRequest(req *schema.ChatCompletionRequest, profileModel string) *schema.RoutingDecision {
	// Extract last user message as prompt
	prompt := ""
	systemPrompt := ""
	for _, msg := range req.Messages {
		content, _ := msg.Content.(string)
		if msg.Role == "user" {
			prompt = content
		} else if msg.Role == "system" {
			systemPrompt = content
		}
	}

	// Estimate tokens
	fullText := systemPrompt + " " + prompt
	estimatedTokens := int64(len(fullText) / 4)

	// Classify
	result := p.router.Classify(prompt, systemPrompt, estimatedTokens)

	// Resolve profile
	profile := p.config.Profile
	if strings.Contains(profileModel, "eco") {
		profile = "eco"
	} else if strings.Contains(profileModel, "premium") {
		profile = "premium"
	}

	tierConfigs := DefaultTierConfigs(profile)

	// Determine tier
	tier := schema.TierMedium // default for ambiguous
	if result.Tier != nil {
		tier = *result.Tier
	}

	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 4096
	}

	decision := SelectModel(tier, result.Confidence, "rules",
		fmt.Sprintf("score=%.2f | %s", result.Score, strings.Join(result.Signals, ", ")),
		tierConfigs, p.catalog, estimatedTokens, maxTokens, profile, result.AgenticScore)

	return &decision
}

func (p *Proxy) chatURL() string {
	if p.baseURL != "" {
		return p.baseURL + "/v1/chat/completions"
	}
	return openRouterChatURL
}

func (p *Proxy) forwardNonStreaming(w http.ResponseWriter, body []byte, model string, dedupKey string) error {
	// Rewrite model in body
	reqBody := rewriteModel(body, model)

	req, err := http.NewRequest("POST", p.chatURL(), bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("HTTP-Referer", "https://github.com/anthropics/clawgo")
	req.Header.Set("X-Title", "ClawGo")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	// Check for retryable errors
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return fmt.Errorf("upstream error: %d", resp.StatusCode)
	}

	// Cache response
	cacheKey := ResponseCacheKey(body)
	p.cache.Set(cacheKey, &CachedLLMResponse{
		Body:    respBody,
		Status:  resp.StatusCode,
		Headers: map[string]string{"content-type": "application/json"},
		Model:   model,
	})

	// Complete dedup
	p.dedup.Complete(dedupKey, &CachedResponse{Status: resp.StatusCode, Body: respBody})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
	return nil
}

func (p *Proxy) forwardStreaming(w http.ResponseWriter, body []byte, model string, dedupKey string) error {
	// Rewrite model and ensure stream=true
	reqBody := rewriteModel(body, model)

	req, err := http.NewRequest("POST", p.chatURL(), bytes.NewReader(reqBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("HTTP-Referer", "https://github.com/anthropics/clawgo")
	req.Header.Set("X-Title", "ClawGo")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}

	// Check for retryable errors before starting stream
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		resp.Body.Close()
		return fmt.Errorf("upstream error: %d", resp.StatusCode)
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}

	// Stream with heartbeat
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Fprintf(w, ": heartbeat\n\n")
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
			case <-done:
				return
			}
		}
	}()

	// Pipe SSE from upstream
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var allData bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		fmt.Fprintf(w, "%s\n", line)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		allData.WriteString(line + "\n")
	}
	resp.Body.Close()
	close(done)

	// Complete dedup (with collected stream data)
	p.dedup.Complete(dedupKey, &CachedResponse{
		Status: resp.StatusCode,
		Body:   allData.Bytes(),
	})

	return nil
}

// rewriteModel replaces the model field in the JSON body.
func rewriteModel(body []byte, model string) []byte {
	var parsed map[string]interface{}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return body
	}
	parsed["model"] = model
	result, err := json.Marshal(parsed)
	if err != nil {
		return body
	}
	return result
}

// isRoutingProfile checks if the model name is a routing profile trigger.
func isRoutingProfile(model string) bool {
	switch strings.ToLower(model) {
	case "auto", "eco", "premium", "clawgo/auto", "clawgo/eco", "clawgo/premium":
		return true
	}
	return false
}

// extractHeaders converts request headers to a simple map.
func extractHeaders(r *http.Request) map[string]string {
	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}
	return headers
}

// writeJSON marshals v as JSON and writes it with the given status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
