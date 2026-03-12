package clawgo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUsageRecorder_RecordLLM_NilDB(t *testing.T) {
	recorder := NewUsageRecorder("")
	err := recorder.RecordLLM(LLMUsageRecord{
		InstanceID:       1,
		UserID:           1,
		RequestID:        "req-1",
		ModelRequested:   "auto",
		ModelResolved:    "gemini-flash",
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
		Status:           "success",
		LatencyMs:        200,
	})
	assert.NoError(t, err)
}

func TestUsageRecorder_RecordSearch_NilDB(t *testing.T) {
	recorder := NewUsageRecorder("")
	err := recorder.RecordSearch(SearchUsageRecord{
		InstanceID:  1,
		UserID:      1,
		RequestID:   "req-2",
		Provider:    "brave",
		Query:       "test query",
		ResultCount: 5,
		Status:      "success",
		LatencyMs:   300,
	})
	assert.NoError(t, err)
}

func TestNewUsageRecorder_EmptyDSN(t *testing.T) {
	recorder := NewUsageRecorder("")
	require.NotNil(t, recorder)
	assert.False(t, recorder.Enabled())
}
