package clawgo

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
)

const (
	openRouterChatURL        = "https://openrouter.ai/api/v1/chat/completions"
	openRouterModelsURLProxy = "https://openrouter.ai/api/v1/models"
	maxFallbackAttempts      = 5
)

var heartbeatInterval = 2 * time.Second

type forwardResult struct {
	Status   int
	Duration time.Duration
	Parsed   transcriptResponse
}

type upstreamAttemptError struct {
	Status   int
	Duration time.Duration
	Err      error
}

func (e *upstreamAttemptError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// Proxy is the HTTP proxy server.
type Proxy struct {
	config  *Config
	router  *Router
	catalog *ModelCatalog
	balance *BalanceMonitor
	session *SessionStore
	dedup   *Deduplicator
	cache   *ResponseCache
	search  *SearchHandler
	usage   *UsageRecorder
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
		client:  &http.Client{Timeout: time.Duration(cfg.RequestTimeoutSec) * time.Second},
	}

	p.setupMux()

	return p
}

// setupMux registers all route handlers on a fresh mux, wrapping
// /v1/chat/completions with the instance auth middleware.
func (p *Proxy) setupMux() {
	p.mux = http.NewServeMux()
	authMw := InstanceAuthMiddleware(p.config.InternalSharedSecret)

	p.mux.Handle("/v1/chat/completions", authMw(http.HandlerFunc(p.handleChatCompletions)))
	if p.search != nil {
		p.mux.Handle("/v1/search", authMw(p.search))
	}
	p.mux.HandleFunc("/health", p.handleHealth)
	p.mux.HandleFunc("/v1/models", p.handleModels)
}

// Run starts the proxy on the configured port.
func (p *Proxy) Run() error {
	ln, err := p.Listen()
	if err != nil {
		return err
	}
	return p.Serve(ln)
}

// Listen binds the configured TCP port and returns the listener.
func (p *Proxy) Listen() (net.Listener, error) {
	addr := fmt.Sprintf(":%d", p.config.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		if isAddrInUse(err) {
			return nil, fmt.Errorf("port %d is already in use; set --port or CLAWGO_PORT to a free port", p.config.Port)
		}
		return nil, err
	}
	return ln, nil
}

// Serve starts serving requests on an existing listener.
func (p *Proxy) Serve(ln net.Listener) error {
	log.Printf("proxy started addr=%s profile=%s models=%d", ln.Addr().String(), p.config.Profile, p.catalog.Count())
	return http.Serve(ln, p.mux)
}

func isAddrInUse(err error) bool {
	var opErr *net.OpError
	if !errors.As(err, &opErr) {
		return false
	}

	var sysErr *os.SyscallError
	if !errors.As(opErr.Err, &sysErr) {
		return false
	}

	return errors.Is(sysErr.Err, syscall.EADDRINUSE)
}

func (p *Proxy) handleHealth(w http.ResponseWriter, r *http.Request) {
	logDebugHTTPRequest(p.config.DebugHTTP, "inbound", r, nil)
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
	logDebugHTTPRequest(p.config.DebugHTTP, "inbound", r, nil)
	modelsURL := openRouterModelsURLProxy
	if p.baseURL != "" {
		modelsURL = p.baseURL + "/v1/models"
	}
	client := &http.Client{Timeout: time.Duration(p.config.ModelsTimeoutSec) * time.Second}
	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: err.Error(), Type: "proxy_error"},
		})
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	logDebugHTTPRequest(p.config.DebugHTTP, "openrouter", req, nil)

	resp, err := client.Do(req)
	if err != nil {
		logDebugHTTPError(p.config.DebugHTTP, "openrouter", req, err)
		writeJSON(w, http.StatusBadGateway, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: err.Error(), Type: "upstream_error"},
		})
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: err.Error(), Type: "upstream_error"},
		})
		return
	}
	logDebugHTTPResponse(p.config.DebugHTTP, "openrouter", resp, respBody)

	for k, v := range resp.Header {
		if len(v) > 0 {
			w.Header().Set(k, v[0])
		}
	}
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
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
		logDebugHTTPRequest(p.config.DebugHTTP, "inbound", r, bodyBytes)
		writeJSON(w, http.StatusBadRequest, schema.ErrorResponse{
			Error: schema.ErrorDetail{Message: "invalid JSON", Type: "invalid_request"},
		})
		return
	}
	logDebugHTTPRequest(p.config.DebugHTTP, "inbound", r, bodyBytes)

	startedAt := time.Now()
	headers := extractHeaders(r)
	sessionID := GetSessionIDFromHeader(headers)
	if sessionID == "" {
		sessionID = DeriveSessionID(chatReq.Messages)
	}

	// 2. Dedup check
	dedupKey := DedupHash(bodyBytes)
	trace := &transcriptTrace{
		ID:             dedupKey,
		SessionID:      sessionID,
		RequestedModel: chatReq.Model,
		Messages:       chatReq.Messages,
		Stream:         chatReq.Stream,
	}
	if cached := p.dedup.GetCached(dedupKey); cached != nil {
		log.Printf("dedup hit key=%s", dedupKey)
		parsed := parseTranscriptResponse(cached.Body, chatReq.Stream)
		trace.Source = "dedup_cache"
		trace.FinalModel = parsed.Model
		trace.Status = cached.Status
		trace.Duration = time.Since(startedAt)
		trace.Assistant = parsed.Assistant
		trace.Usage = parsed.Usage
		trace.FinishReason = parsed.FinishReason
		trace.Error = parsed.Error
		logDebugTranscript(p.config.DebugTranscript, trace)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(cached.Status)
		w.Write(cached.Body)
		return
	}
	if ch := p.dedup.GetInflight(dedupKey); ch != nil {
		log.Printf("dedup waiting key=%s", dedupKey)
		result := <-ch
		parsed := parseTranscriptResponse(result.Body, chatReq.Stream)
		trace.Source = "dedup_wait"
		trace.FinalModel = parsed.Model
		trace.Status = result.Status
		trace.Duration = time.Since(startedAt)
		trace.Assistant = parsed.Assistant
		trace.Usage = parsed.Usage
		trace.FinishReason = parsed.FinishReason
		trace.Error = parsed.Error
		logDebugTranscript(p.config.DebugTranscript, trace)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(result.Status)
		w.Write(result.Body)
		return
	}
	p.dedup.MarkInflight(dedupKey)

	// 3. Resolve model
	model := chatReq.Model
	profile, isAutoRoute := p.config.ResolveRoutingProfile(model)
	var tierConfigs map[schema.Tier]schema.TierConfig

	var decision *schema.RoutingDecision
	if isAutoRoute {
		tierConfigs = p.config.TierConfigs(profile)
		decision = p.routeRequest(&chatReq, profile, tierConfigs)
		model = decision.Model
		trace.Tier = string(decision.Tier)
		trace.Confidence = decision.Confidence
		trace.RouteReason = decision.Reasoning
	}

	// 4. Session pinning
	if sessionID != "" && isAutoRoute {
		if entry := p.session.Get(sessionID); entry != nil {
			model = entry.Model
			log.Printf("session pinned session=%s model=%s", sessionID, model)
		}
	}
	trace.SelectedModel = model

	// 5. Response cache check (non-streaming only)
	if !chatReq.Stream {
		cacheKey := ResponseCacheKey(bodyBytes)
		if cached := p.cache.Get(cacheKey); cached != nil {
			log.Printf("cache hit model=%s", cached.Model)
			p.dedup.Complete(dedupKey, &CachedResponse{Status: cached.Status, Body: cached.Body})
			parsed := parseTranscriptResponse(cached.Body, false)
			trace.Source = "cache"
			trace.FinalModel = firstNonEmpty(cached.Model, parsed.Model)
			trace.Status = cached.Status
			trace.Duration = time.Since(startedAt)
			trace.Assistant = parsed.Assistant
			trace.Usage = parsed.Usage
			trace.FinishReason = parsed.FinishReason
			trace.Error = parsed.Error
			logDebugTranscript(p.config.DebugTranscript, trace)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(cached.Status)
			w.Write(cached.Body)
			return
		}
	}

	// 6. Get fallback chain
	var fallbackChain []string
	if decision != nil {
		fallbackChain = GetFallbackChain(decision.Tier, tierConfigs)
	} else {
		fallbackChain = []string{model}
	}

	// 7. Forward with fallback
	var lastErr error
	var attempts []transcriptAttempt
	for i, tryModel := range fallbackChain {
		if i >= maxFallbackAttempts {
			break
		}

		var result *forwardResult
		if chatReq.Stream {
			result, err = p.forwardStreaming(w, bodyBytes, tryModel, dedupKey)
		} else {
			result, err = p.forwardNonStreaming(w, bodyBytes, tryModel, dedupKey)
		}

		if err == nil {
			attempts = append(attempts, transcriptAttempt{
				Model:    tryModel,
				Status:   result.Status,
				Duration: result.Duration,
			})
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
			trace.Source = "upstream"
			trace.FinalModel = firstNonEmpty(result.Parsed.Model, tryModel)
			trace.Status = result.Status
			trace.Duration = time.Since(startedAt)
			trace.Assistant = result.Parsed.Assistant
			trace.Usage = result.Parsed.Usage
			trace.FinishReason = result.Parsed.FinishReason
			trace.Error = result.Parsed.Error
			trace.Attempts = attempts
			logDebugTranscript(p.config.DebugTranscript, trace)
			if p.usage != nil && p.usage.Enabled() {
				instanceID := InstanceIDFromContext(r.Context())
				uid := UserIDFromContext(r.Context())
				usageStatus := "success"
				if result.Status >= 400 {
					usageStatus = "error"
				}
				var promptTok, completionTok, totalTok int64
				if result.Parsed.Usage != nil {
					promptTok = result.Parsed.Usage.PromptTokens
					completionTok = result.Parsed.Usage.CompletionTokens
					totalTok = result.Parsed.Usage.TotalTokens
				}
				go p.usage.RecordLLM(LLMUsageRecord{
					InstanceID:       instanceID,
					UserID:           uid,
					RequestID:        dedupKey,
					ModelRequested:   chatReq.Model,
					ModelResolved:    tryModel,
					PromptTokens:     promptTok,
					CompletionTokens: completionTok,
					TotalTokens:      totalTok,
					Status:           usageStatus,
					LatencyMs:        result.Duration.Milliseconds(),
				})
			}
			return
		}

		attempt := transcriptAttempt{Model: tryModel}
		if attemptErr, ok := err.(*upstreamAttemptError); ok {
			attempt.Status = attemptErr.Status
			attempt.Duration = attemptErr.Duration
			attempt.Err = attemptErr.Error()
		} else if err != nil {
			attempt.Err = err.Error()
		}
		attempts = append(attempts, attempt)
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
	trace.Source = "upstream_error"
	trace.Status = http.StatusBadGateway
	trace.Duration = time.Since(startedAt)
	trace.Error = fmt.Sprintf("all models failed: %v", lastErr)
	trace.Attempts = attempts
	logDebugTranscript(p.config.DebugTranscript, trace)
	if p.usage != nil && p.usage.Enabled() {
		instanceID := InstanceIDFromContext(r.Context())
		uid := UserIDFromContext(r.Context())
		go p.usage.RecordLLM(LLMUsageRecord{
			InstanceID:     instanceID,
			UserID:         uid,
			RequestID:      dedupKey,
			ModelRequested: chatReq.Model,
			Status:         "error",
			ErrorCode:      "all_models_failed",
			LatencyMs:      time.Since(startedAt).Milliseconds(),
		})
	}
}

func (p *Proxy) routeRequest(req *schema.ChatCompletionRequest, profile string, tierConfigs map[schema.Tier]schema.TierConfig) *schema.RoutingDecision {
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

func (p *Proxy) forwardNonStreaming(w http.ResponseWriter, body []byte, model string, dedupKey string) (*forwardResult, error) {
	// Rewrite model in body
	reqBody := rewriteModel(body, model)
	startedAt := time.Now()

	req, err := http.NewRequest("POST", p.chatURL(), bytes.NewReader(reqBody))
	if err != nil {
		return nil, &upstreamAttemptError{Duration: time.Since(startedAt), Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("HTTP-Referer", "https://github.com/anthropics/clawgo")
	req.Header.Set("X-Title", "ClawGo")
	logDebugHTTPRequest(p.config.DebugHTTP, "openrouter", req, reqBody)

	resp, err := p.client.Do(req)
	if err != nil {
		logDebugHTTPError(p.config.DebugHTTP, "openrouter", req, err)
		return nil, &upstreamAttemptError{Duration: time.Since(startedAt), Err: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &upstreamAttemptError{Duration: time.Since(startedAt), Err: err}
	}
	logDebugHTTPResponse(p.config.DebugHTTP, "openrouter", resp, respBody)

	// Check for retryable errors
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		return nil, &upstreamAttemptError{
			Status:   resp.StatusCode,
			Duration: time.Since(startedAt),
			Err:      fmt.Errorf("upstream error: %d", resp.StatusCode),
		}
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
	return &forwardResult{
		Status:   resp.StatusCode,
		Duration: time.Since(startedAt),
		Parsed:   parseTranscriptResponse(respBody, false),
	}, nil
}

func (p *Proxy) forwardStreaming(w http.ResponseWriter, body []byte, model string, dedupKey string) (*forwardResult, error) {
	// Rewrite model and ensure stream=true
	reqBody := rewriteModel(body, model)
	startedAt := time.Now()

	req, err := http.NewRequest("POST", p.chatURL(), bytes.NewReader(reqBody))
	if err != nil {
		return nil, &upstreamAttemptError{Duration: time.Since(startedAt), Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.config.APIKey)
	req.Header.Set("HTTP-Referer", "https://github.com/anthropics/clawgo")
	req.Header.Set("X-Title", "ClawGo")
	logDebugHTTPRequest(p.config.DebugHTTP, "openrouter", req, reqBody)

	resp, err := p.client.Do(req)
	if err != nil {
		logDebugHTTPError(p.config.DebugHTTP, "openrouter", req, err)
		return nil, &upstreamAttemptError{Duration: time.Since(startedAt), Err: err}
	}

	// Check for retryable errors before starting stream
	if resp.StatusCode == 429 || resp.StatusCode >= 500 {
		logDebugHTTPResponse(p.config.DebugHTTP, "openrouter", resp, nil)
		resp.Body.Close()
		return nil, &upstreamAttemptError{
			Status:   resp.StatusCode,
			Duration: time.Since(startedAt),
			Err:      fmt.Errorf("upstream error: %d", resp.StatusCode),
		}
	}
	logDebugHTTPResponse(p.config.DebugHTTP, "openrouter", resp, nil)

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	var writeMu sync.Mutex
	flushLocked := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}
	writeLine := func(line string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		fmt.Fprintf(w, "%s\n", line)
		flushLocked()
	}
	writeRaw := func(raw string) {
		writeMu.Lock()
		defer writeMu.Unlock()
		fmt.Fprint(w, raw)
		flushLocked()
	}
	writeMu.Lock()
	flushLocked()
	writeMu.Unlock()

	// Stream with heartbeat
	done := make(chan struct{})
	interval := heartbeatInterval
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				writeRaw(": heartbeat\n\n")
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
		logDebugHTTPStreamChunk(p.config.DebugHTTP, "openrouter", p.chatURL(), line)
		writeLine(line)
		allData.WriteString(line + "\n")
	}
	resp.Body.Close()
	close(done)

	// Complete dedup (with collected stream data)
	p.dedup.Complete(dedupKey, &CachedResponse{
		Status: resp.StatusCode,
		Body:   allData.Bytes(),
	})

	return &forwardResult{
		Status:   resp.StatusCode,
		Duration: time.Since(startedAt),
		Parsed:   parseTranscriptResponse(allData.Bytes(), true),
	}, nil
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
