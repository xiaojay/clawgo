package clawgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxDebugHTTPBodyBytes = 4096

func logDebugHTTPRequest(enabled bool, kind string, r *http.Request, body []byte) {
	if !enabled || r == nil {
		return
	}

	log.Printf("debug_http %s_request method=%s url=%s remote=%s headers=%s",
		kind, r.Method, requestURLForLog(r), r.RemoteAddr, formatHeadersForLog(r.Header))
	if len(body) > 0 {
		log.Printf("debug_http %s_request_body body=%s", kind, formatBodyForLog(body))
	}
}

func logDebugHTTPResponse(enabled bool, kind string, resp *http.Response, body []byte) {
	if !enabled || resp == nil {
		return
	}

	url := ""
	if resp.Request != nil && resp.Request.URL != nil {
		url = resp.Request.URL.String()
	}

	log.Printf("debug_http %s_response status=%d url=%s headers=%s",
		kind, resp.StatusCode, url, formatHeadersForLog(resp.Header))
	if len(body) > 0 {
		log.Printf("debug_http %s_response_body body=%s", kind, formatBodyForLog(body))
	}
}

func logDebugHTTPError(enabled bool, kind string, r *http.Request, err error) {
	if !enabled || err == nil {
		return
	}

	url := ""
	method := ""
	if r != nil {
		url = requestURLForLog(r)
		method = r.Method
	}

	log.Printf("debug_http %s_error method=%s url=%s err=%v", kind, method, url, err)
}

func logDebugHTTPStreamChunk(enabled bool, kind string, url string, line string) {
	if !enabled {
		return
	}
	log.Printf("debug_http %s_stream_chunk url=%s chunk=%s", kind, url, formatBodyForLog([]byte(line)))
}

func requestURLForLog(r *http.Request) string {
	if r == nil || r.URL == nil {
		return ""
	}
	if r.URL.Scheme != "" && r.URL.Host != "" {
		return r.URL.String()
	}
	return r.URL.RequestURI()
}

func formatHeadersForLog(headers http.Header) string {
	if len(headers) == 0 {
		return "-"
	}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		values := headers.Values(key)
		redacted := make([]string, len(values))
		for i, value := range values {
			redacted[i] = redactHeaderValue(key, value)
		}
		parts = append(parts, fmt.Sprintf("%s=%q", key, redacted))
	}
	return strings.Join(parts, " ")
}

func redactHeaderValue(key string, value string) string {
	switch strings.ToLower(key) {
	case "authorization", "proxy-authorization":
		fields := strings.Fields(value)
		if len(fields) > 0 {
			return fields[0] + " <redacted>"
		}
		return "<redacted>"
	case "cookie", "set-cookie", "x-api-key":
		return "<redacted>"
	default:
		return value
	}
}

func formatBodyForLog(body []byte) string {
	if len(body) == 0 {
		return "<empty>"
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "<empty>"
	}

	display := trimmed
	if json.Valid(trimmed) {
		var compact bytes.Buffer
		if err := json.Compact(&compact, trimmed); err == nil {
			display = compact.Bytes()
		}
	}

	truncated := 0
	if len(display) > maxDebugHTTPBodyBytes {
		truncated = len(display) - maxDebugHTTPBodyBytes
		display = display[:maxDebugHTTPBodyBytes]
	}

	var text string
	if utf8.Valid(display) {
		text = string(display)
	} else {
		text = fmt.Sprintf("<%d bytes binary>", len(display))
	}

	text = strings.ReplaceAll(text, "\n", "\\n")
	text = strings.ReplaceAll(text, "\r", "\\r")
	if truncated > 0 {
		text = fmt.Sprintf("%s...<truncated %d bytes>", text, truncated)
	}
	return text
}
