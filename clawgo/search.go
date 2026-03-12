package clawgo

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultBraveSearchURL = "https://api.search.brave.com/res/v1/web/search"

type SearchRequest struct {
	Query      string `json:"query"`
	Count      int    `json:"count,omitempty"`
	Country    string `json:"country,omitempty"`
	Language   string `json:"language,omitempty"`
	SafeSearch string `json:"safesearch,omitempty"`
}

type SearchResult struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description"`
}

type SearchResponse struct {
	Results  []SearchResult `json:"results"`
	Provider string         `json:"provider"`
	Query    string         `json:"query"`
}

type SearchHandler struct {
	braveAPIKey string
	braveURL    string
	client      *http.Client
	usage       *UsageRecorder
}

func NewSearchHandler(braveAPIKey, braveURL string) *SearchHandler {
	if braveURL == "" {
		braveURL = defaultBraveSearchURL
	}
	return &SearchHandler{
		braveAPIKey: braveAPIKey,
		braveURL:    braveURL,
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

func (sh *SearchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]interface{}{
			"error": map[string]string{"message": "method not allowed", "type": "invalid_request"},
		})
		return
	}

	if sh.braveAPIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]interface{}{
			"error": map[string]string{"message": "search service not configured", "type": "service_unavailable"},
		})
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": map[string]string{"message": "failed to read request body", "type": "invalid_request"},
		})
		return
	}

	var searchReq SearchRequest
	if err := json.Unmarshal(body, &searchReq); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": map[string]string{"message": "invalid JSON", "type": "invalid_request"},
		})
		return
	}

	if searchReq.Query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]interface{}{
			"error": map[string]string{"message": "query is required", "type": "invalid_request"},
		})
		return
	}

	if searchReq.Count == 0 {
		searchReq.Count = 5
	}
	if searchReq.Count > 20 {
		searchReq.Count = 20
	}

	startedAt := time.Now()
	results, err := sh.queryBrave(searchReq)
	latency := time.Since(startedAt)
	if err != nil {
		log.Printf("brave search error latency=%dms: %v", latency.Milliseconds(), err)
		writeJSON(w, http.StatusBadGateway, map[string]interface{}{
			"error": map[string]string{"message": "search upstream error", "type": "upstream_error"},
		})
		if sh.usage != nil && sh.usage.Enabled() {
			instanceID := InstanceIDFromContext(r.Context())
			uid := UserIDFromContext(r.Context())
			go sh.usage.RecordSearch(SearchUsageRecord{
				InstanceID: instanceID,
				UserID:     uid,
				RequestID:  fmt.Sprintf("search-%d", time.Now().UnixNano()),
				Provider:   "brave",
				Query:      searchReq.Query,
				Status:     "error",
				ErrorCode:  "upstream_error",
				LatencyMs:  latency.Milliseconds(),
			})
		}
		return
	}

	log.Printf("brave search query=%q results=%d latency=%dms", searchReq.Query, len(results), latency.Milliseconds())

	writeJSON(w, http.StatusOK, SearchResponse{
		Results:  results,
		Provider: "brave",
		Query:    searchReq.Query,
	})
	if sh.usage != nil && sh.usage.Enabled() {
		instanceID := InstanceIDFromContext(r.Context())
		uid := UserIDFromContext(r.Context())
		go sh.usage.RecordSearch(SearchUsageRecord{
			InstanceID:  instanceID,
			UserID:      uid,
			RequestID:   fmt.Sprintf("search-%d", time.Now().UnixNano()),
			Provider:    "brave",
			Query:       searchReq.Query,
			ResultCount: len(results),
			Status:      "success",
			LatencyMs:   latency.Milliseconds(),
		})
	}
}

func (sh *SearchHandler) queryBrave(searchReq SearchRequest) ([]SearchResult, error) {
	params := url.Values{}
	params.Set("q", searchReq.Query)
	params.Set("count", strconv.Itoa(searchReq.Count))
	if searchReq.Country != "" {
		params.Set("country", searchReq.Country)
	}
	if searchReq.Language != "" {
		params.Set("search_lang", searchReq.Language)
	}
	if searchReq.SafeSearch != "" {
		params.Set("safesearch", searchReq.SafeSearch)
	}

	reqURL := sh.braveURL + "?" + params.Encode()
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", sh.braveAPIKey)

	resp, err := sh.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("brave returned %d: %s", resp.StatusCode, string(body))
	}

	var braveResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&braveResp); err != nil {
		return nil, fmt.Errorf("decode brave response: %w", err)
	}

	results := make([]SearchResult, 0, len(braveResp.Web.Results))
	for _, r := range braveResp.Web.Results {
		results = append(results, SearchResult{
			Title:       r.Title,
			URL:         r.URL,
			Description: r.Description,
		})
	}
	return results, nil
}
