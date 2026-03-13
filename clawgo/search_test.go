package clawgo

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSearchHandler_Success(t *testing.T) {
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "brave-key", r.Header.Get("X-Subscription-Token"))
		assert.Equal(t, "test query", r.URL.Query().Get("q"))
		assert.Equal(t, "5", r.URL.Query().Get("count"))
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"web": map[string]interface{}{
				"results": []map[string]interface{}{
					{"title": "Result 1", "url": "https://example.com", "description": "desc"},
				},
			},
		})
	}))
	defer brave.Close()

	sh := NewSearchHandler("brave-key", brave.URL)

	body, _ := json.Marshal(SearchRequest{Query: "test query", Count: 5})
	req := httptest.NewRequest("POST", "/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp SearchResponse
	err := json.Unmarshal(w.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.Len(t, resp.Results, 1)
	assert.Equal(t, "Result 1", resp.Results[0].Title)
}

func TestSearchHandler_MissingQuery(t *testing.T) {
	sh := NewSearchHandler("brave-key", "")
	body, _ := json.Marshal(SearchRequest{})
	req := httptest.NewRequest("POST", "/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchHandler_NoBraveKey(t *testing.T) {
	sh := NewSearchHandler("", "")
	body, _ := json.Marshal(SearchRequest{Query: "test"})
	req := httptest.NewRequest("POST", "/v1/search", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestSearchHandler_MethodNotAllowed(t *testing.T) {
	sh := NewSearchHandler("brave-key", "")
	req := httptest.NewRequest("GET", "/v1/search", nil)
	w := httptest.NewRecorder()
	sh.ServeHTTP(w, req)

	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}
