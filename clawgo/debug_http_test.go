package clawgo

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatHeadersForLogRedactsSecrets(t *testing.T) {
	headers := http.Header{}
	headers.Set("Authorization", "Bearer sk-secret")
	headers.Set("X-Api-Key", "top-secret")
	headers.Set("Content-Type", "application/json")

	formatted := formatHeadersForLog(headers)

	assert.Contains(t, formatted, `Authorization=["Bearer <redacted>"]`)
	assert.Contains(t, formatted, `X-Api-Key=["<redacted>"]`)
	assert.Contains(t, formatted, `Content-Type=["application/json"]`)
	assert.NotContains(t, formatted, "sk-secret")
	assert.NotContains(t, formatted, "top-secret")
}

func TestFormatBodyForLogTruncatesAndCompactsJSON(t *testing.T) {
	body := []byte("{\n  \"message\": \"" + strings.Repeat("a", maxDebugHTTPBodyBytes) + "\"\n}")

	formatted := formatBodyForLog(body)

	assert.Contains(t, formatted, `{"message":"`)
	assert.Contains(t, formatted, "<truncated")
	assert.NotContains(t, formatted, "\n")
}
