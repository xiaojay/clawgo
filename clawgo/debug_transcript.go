package clawgo

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/anthropics/clawgo/clawgo/schema"
)

const maxTranscriptSectionChars = 2400

var transcriptLogger = log.Printf

var transcriptSeparatorColors = []string{
	"\033[38;5;196m",
	"\033[38;5;202m",
	"\033[38;5;226m",
	"\033[38;5;82m",
	"\033[38;5;51m",
	"\033[38;5;21m",
	"\033[38;5;201m",
}

const transcriptSeparatorReset = "\033[0m"

type transcriptTrace struct {
	ID             string
	SessionID      string
	Source         string
	RequestedModel string
	SelectedModel  string
	FinalModel     string
	Tier           string
	Confidence     float64
	RouteReason    string
	Status         int
	Stream         bool
	Duration       time.Duration
	Messages       []schema.ChatMessage
	Assistant      string
	Usage          *schema.Usage
	FinishReason   string
	Error          string
	Attempts       []transcriptAttempt
}

type transcriptAttempt struct {
	Model    string
	Status   int
	Duration time.Duration
	Err      string
}

type transcriptResponse struct {
	Model        string
	Assistant    string
	Usage        *schema.Usage
	FinishReason string
	Error        string
}

func logDebugTranscript(enabled bool, trace *transcriptTrace) {
	if !enabled || trace == nil {
		return
	}

	var b strings.Builder
	fmt.Fprintf(&b, "llm_transcript id=%s", trace.ID)
	if trace.SessionID != "" {
		fmt.Fprintf(&b, " session=%s", trace.SessionID)
	}
	if trace.Source != "" {
		fmt.Fprintf(&b, " source=%s", trace.Source)
	}
	if trace.RequestedModel != "" {
		fmt.Fprintf(&b, " requested=%s", trace.RequestedModel)
	}
	if trace.SelectedModel != "" {
		fmt.Fprintf(&b, " selected=%s", trace.SelectedModel)
	}
	if trace.FinalModel != "" {
		fmt.Fprintf(&b, " final=%s", trace.FinalModel)
	}
	if trace.Tier != "" {
		fmt.Fprintf(&b, " tier=%s", trace.Tier)
	}
	if trace.Confidence > 0 {
		fmt.Fprintf(&b, " confidence=%.2f", trace.Confidence)
	}
	if trace.Status != 0 {
		fmt.Fprintf(&b, " status=%d", trace.Status)
	}
	fmt.Fprintf(&b, " stream=%t duration_ms=%d", trace.Stream, trace.Duration.Milliseconds())

	if len(trace.Attempts) > 0 {
		fmt.Fprintf(&b, "\nattempts: %s", formatTranscriptAttempts(trace.Attempts))
	}
	if trace.RouteReason != "" {
		fmt.Fprintf(&b, "\nroute: %s", truncateForTranscript(singleLine(trace.RouteReason), maxTranscriptSectionChars))
	}

	for _, msg := range trace.Messages {
		fmt.Fprintf(&b, "\n\n[%s]\n%s", transcriptMessageLabel(msg), formatTranscriptMessageContent(msg.Content))
	}

	if trace.Assistant != "" || trace.Error == "" {
		fmt.Fprintf(&b, "\n\n[assistant]\n%s", truncateForTranscript(strings.TrimSpace(trace.Assistant), maxTranscriptSectionChars))
	}
	if trace.Error != "" {
		fmt.Fprintf(&b, "\n\n[error]\n%s", truncateForTranscript(strings.TrimSpace(trace.Error), maxTranscriptSectionChars))
	}

	var meta []string
	if trace.Usage != nil {
		meta = append(meta, formatTranscriptUsage(trace.Usage)...)
	}
	if trace.FinishReason != "" {
		meta = append(meta, "finish="+trace.FinishReason)
	}
	if len(meta) > 0 {
		fmt.Fprintf(&b, "\n\n%s", strings.Join(meta, " "))
	}
	fmt.Fprintf(&b, "\n\n%s", colorfulTranscriptSeparator(72))

	transcriptLogger("%s", b.String())
}

func formatTranscriptAttempts(attempts []transcriptAttempt) string {
	parts := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		label := attempt.Model
		if label == "" {
			label = "<unknown>"
		}
		if attempt.Status > 0 {
			label = fmt.Sprintf("%s(%d,%dms)", label, attempt.Status, attempt.Duration.Milliseconds())
		} else if attempt.Err != "" {
			label = fmt.Sprintf("%s(err=%s,%dms)", label,
				truncateForTranscript(singleLine(attempt.Err), 120), attempt.Duration.Milliseconds())
		} else {
			label = fmt.Sprintf("%s(%dms)", label, attempt.Duration.Milliseconds())
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, " -> ")
}

func transcriptMessageLabel(msg schema.ChatMessage) string {
	label := msg.Role
	if label == "" {
		label = "message"
	}
	if msg.Name != "" {
		label += ":" + msg.Name
	}
	return label
}

func formatTranscriptMessageContent(content interface{}) string {
	switch v := content.(type) {
	case nil:
		return "<empty>"
	case string:
		return truncateForTranscript(strings.TrimSpace(v), maxTranscriptSectionChars)
	default:
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return truncateForTranscript(fmt.Sprintf("%v", v), maxTranscriptSectionChars)
		}
		return truncateForTranscript(string(bytes.TrimSpace(data)), maxTranscriptSectionChars)
	}
}

func truncateForTranscript(s string, maxChars int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "<empty>"
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return fmt.Sprintf("%s\n...<truncated %d chars>", string(runes[:maxChars]), len(runes)-maxChars)
}

func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return strings.Join(strings.Fields(s), " ")
}

func parseTranscriptResponse(body []byte, stream bool) transcriptResponse {
	if len(body) == 0 {
		return transcriptResponse{}
	}
	if stream {
		return parseStreamingTranscriptResponse(body)
	}
	return parseJSONTranscriptResponse(body)
}

func parseJSONTranscriptResponse(body []byte) transcriptResponse {
	var resp schema.ChatCompletionResponse
	if err := json.Unmarshal(body, &resp); err == nil {
		return transcriptResponse{
			Model:        resp.Model,
			Assistant:    extractAssistantMessage(resp.Choices),
			Usage:        cloneUsage(resp.Usage),
			FinishReason: extractFinishReason(resp.Choices),
		}
	}

	var errResp schema.ErrorResponse
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error.Message != "" {
		return transcriptResponse{Error: errResp.Error.Message}
	}

	return transcriptResponse{Error: truncateForTranscript(string(bytes.TrimSpace(body)), maxTranscriptSectionChars)}
}

func parseStreamingTranscriptResponse(body []byte) transcriptResponse {
	lines := strings.Split(string(body), "\n")
	var assistant strings.Builder
	result := transcriptResponse{}

	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}

		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var resp schema.ChatCompletionResponse
		if err := json.Unmarshal([]byte(payload), &resp); err != nil {
			continue
		}

		if result.Model == "" {
			result.Model = resp.Model
		}
		if resp.Usage != nil {
			result.Usage = cloneUsage(resp.Usage)
		}

		for _, choice := range resp.Choices {
			if choice.Delta != nil {
				if deltaText := contentToTranscriptText(choice.Delta.Content); strings.TrimSpace(deltaText) != "" {
					assistant.WriteString(deltaText)
				}
			} else if assistant.Len() == 0 && choice.Message != nil {
				if messageText := contentToTranscriptText(choice.Message.Content); strings.TrimSpace(messageText) != "" {
					assistant.WriteString(messageText)
				}
			}
			if choice.FinishReason != nil && *choice.FinishReason != "" {
				result.FinishReason = *choice.FinishReason
			}
		}
	}

	result.Assistant = assistant.String()
	return result
}

func extractAssistantMessage(choices []schema.Choice) string {
	for _, choice := range choices {
		if choice.Message != nil {
			if text := contentToTranscriptText(choice.Message.Content); strings.TrimSpace(text) != "" {
				return text
			}
		}
		if choice.Delta != nil {
			if text := contentToTranscriptText(choice.Delta.Content); strings.TrimSpace(text) != "" {
				return text
			}
		}
	}
	return ""
}

func extractFinishReason(choices []schema.Choice) string {
	for _, choice := range choices {
		if choice.FinishReason != nil && *choice.FinishReason != "" {
			return *choice.FinishReason
		}
	}
	return ""
}

func contentToTranscriptText(content interface{}) string {
	switch v := content.(type) {
	case nil:
		return ""
	case string:
		return v
	default:
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(bytes.TrimSpace(data))
	}
}

func cloneUsage(usage *schema.Usage) *schema.Usage {
	if usage == nil {
		return nil
	}

	cloned := &schema.Usage{
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}
	if usage.Cost != nil {
		value := *usage.Cost
		cloned.Cost = &value
	}
	if usage.PromptTokensDetails != nil {
		details := *usage.PromptTokensDetails
		cloned.PromptTokensDetails = &details
	}
	if usage.CompletionTokensDetails != nil {
		details := *usage.CompletionTokensDetails
		cloned.CompletionTokensDetails = &details
	}
	if usage.CostDetails != nil {
		details := &schema.CostDetails{}
		if usage.CostDetails.UpstreamInferenceCost != nil {
			value := *usage.CostDetails.UpstreamInferenceCost
			details.UpstreamInferenceCost = &value
		}
		cloned.CostDetails = details
	}
	return cloned
}

func colorfulTranscriptSeparator(width int) string {
	if width <= 0 {
		width = 72
	}

	var b strings.Builder
	for i := 0; i < width; i++ {
		b.WriteString(transcriptSeparatorColors[i%len(transcriptSeparatorColors)])
		b.WriteByte('=')
	}
	b.WriteString(transcriptSeparatorReset)
	return b.String()
}

func formatTranscriptUsage(usage *schema.Usage) []string {
	if usage == nil {
		return nil
	}

	lines := []string{
		fmt.Sprintf("usage: prompt=%d completion=%d total=%d",
			usage.PromptTokens, usage.CompletionTokens, usage.TotalTokens),
	}

	if usage.PromptTokensDetails != nil {
		lines = append(lines, fmt.Sprintf("cache: hit=%t cached_tokens=%d cache_write_tokens=%d",
			usage.PromptTokensDetails.CachedTokens > 0,
			usage.PromptTokensDetails.CachedTokens,
			usage.PromptTokensDetails.CacheWriteTokens))
	}

	if usage.CompletionTokensDetails != nil && usage.CompletionTokensDetails.ReasoningTokens > 0 {
		lines = append(lines, fmt.Sprintf("reasoning: tokens=%d",
			usage.CompletionTokensDetails.ReasoningTokens))
	}

	if usage.Cost != nil || (usage.CostDetails != nil && usage.CostDetails.UpstreamInferenceCost != nil) {
		var parts []string
		if usage.Cost != nil {
			parts = append(parts, fmt.Sprintf("total=$%.6f", *usage.Cost))
		}
		if usage.CostDetails != nil && usage.CostDetails.UpstreamInferenceCost != nil {
			parts = append(parts, fmt.Sprintf("upstream_inference=$%.6f", *usage.CostDetails.UpstreamInferenceCost))
		}
		lines = append(lines, "cost: "+strings.Join(parts, " "))
	}

	return lines
}
