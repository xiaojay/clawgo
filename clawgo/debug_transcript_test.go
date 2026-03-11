package clawgo

import (
	"testing"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
	"github.com/stretchr/testify/assert"
)

func TestParseStreamingTranscriptResponse(t *testing.T) {
	body := []byte("data: {\"id\":\"chatcmpl-1\",\"model\":\"openai/gpt-4o\",\"choices\":[{\"delta\":{\"role\":\"assistant\"},\"index\":0}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"model\":\"openai/gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"Hello\"},\"index\":0}]}\n" +
		"data: {\"id\":\"chatcmpl-1\",\"model\":\"openai/gpt-4o\",\"choices\":[{\"delta\":{\"content\":\" world\"},\"index\":0,\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n" +
		"data: [DONE]\n")

	parsed := parseTranscriptResponse(body, true)

	assert.Equal(t, "openai/gpt-4o", parsed.Model)
	assert.Equal(t, "Hello world", parsed.Assistant)
	assert.Equal(t, "stop", parsed.FinishReason)
	if assert.NotNil(t, parsed.Usage) {
		assert.Equal(t, int64(7), parsed.Usage.TotalTokens)
	}
}

func TestFormatTranscriptAttempts(t *testing.T) {
	formatted := formatTranscriptAttempts([]transcriptAttempt{
		{Model: "anthropic/claude-sonnet-4", Status: 429, Duration: 120 * time.Millisecond},
		{Model: "openai/o3", Status: 200, Duration: 980 * time.Millisecond},
	})

	assert.Equal(t, "anthropic/claude-sonnet-4(429,120ms) -> openai/o3(200,980ms)", formatted)
}

func TestFormatTranscriptMessageContent(t *testing.T) {
	content := []map[string]interface{}{
		{"type": "text", "text": "hello"},
	}

	formatted := formatTranscriptMessageContent(content)

	assert.Contains(t, formatted, "\"type\": \"text\"")
	assert.Contains(t, formatted, "\"text\": \"hello\"")
}

func TestFormatTranscriptUsage(t *testing.T) {
	cost := 0.001234
	upstreamCost := 0.001111
	lines := formatTranscriptUsage(&schema.Usage{
		PromptTokens:     100,
		CompletionTokens: 20,
		TotalTokens:      120,
		Cost:             &cost,
		PromptTokensDetails: &schema.PromptTokensDetails{
			CachedTokens:     64,
			CacheWriteTokens: 128,
		},
		CompletionTokensDetails: &schema.CompletionTokensDetails{
			ReasoningTokens: 7,
		},
		CostDetails: &schema.CostDetails{
			UpstreamInferenceCost: &upstreamCost,
		},
	})

	assert.Contains(t, lines, "usage: prompt=100 completion=20 total=120")
	assert.Contains(t, lines, "cache: hit=true cached_tokens=64 cache_write_tokens=128")
	assert.Contains(t, lines, "reasoning: tokens=7")
	assert.Contains(t, lines, "cost: total=$0.001234 upstream_inference=$0.001111")
}

func TestLogDebugTranscriptIncludesConversation(t *testing.T) {
	cost := 0.002345
	trace := &transcriptTrace{
		ID:             "abcd1234",
		SessionID:      "sess1",
		Source:         "upstream",
		RequestedModel: "auto",
		SelectedModel:  "anthropic/claude-sonnet-4",
		FinalModel:     "anthropic/claude-sonnet-4",
		Tier:           "REASONING",
		Confidence:     0.92,
		Status:         200,
		Duration:       1500 * time.Millisecond,
		Messages: []schema.ChatMessage{
			{Role: "system", Content: "you are helpful"},
			{Role: "user", Content: "write a haiku"},
		},
		Assistant:    "Quiet autumn breeze",
		FinishReason: "stop",
		Usage: &schema.Usage{
			PromptTokens:     10,
			CompletionTokens: 8,
			TotalTokens:      18,
			Cost:             &cost,
			PromptTokensDetails: &schema.PromptTokensDetails{
				CachedTokens: 4,
			},
			CompletionTokensDetails: &schema.CompletionTokensDetails{
				ReasoningTokens: 2,
			},
		},
	}

	var logged string
	original := transcriptLogger
	transcriptLogger = func(format string, args ...interface{}) {
		logged = format
		if len(args) > 0 {
			logged = ""
			for _, arg := range args {
				if s, ok := arg.(string); ok {
					logged += s
				}
			}
		}
	}
	defer func() { transcriptLogger = original }()

	logDebugTranscript(true, trace)

	assert.Contains(t, logged, "llm_transcript id=abcd1234")
	assert.Contains(t, logged, "[user]\nwrite a haiku")
	assert.Contains(t, logged, "[assistant]\nQuiet autumn breeze")
	assert.Contains(t, logged, "usage: prompt=10 completion=8 total=18")
	assert.Contains(t, logged, "cache: hit=true cached_tokens=4 cache_write_tokens=0")
	assert.Contains(t, logged, "reasoning: tokens=2")
	assert.Contains(t, logged, "cost: total=$0.002345")
	assert.Contains(t, logged, "\033[38;5;196m")
	assert.Contains(t, logged, "\033[0m")
}
