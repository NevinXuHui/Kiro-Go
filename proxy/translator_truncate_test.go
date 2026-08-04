package proxy

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestClaudeToKiroTruncatesOversizedHistory builds a conversation whose history
// far exceeds the upstream input limit and verifies the converted payload is
// trimmed below maxPayloadBytes, that a truncation placeholder is inserted, and
// that the current message is preserved.
func TestClaudeToKiroTruncatesOversizedHistory(t *testing.T) {
	// ~2KB chunk repeated across many turns to blow past the byte limit.
	big := strings.Repeat("lorem ipsum dolor sit amet ", 80) // ~2.1KB

	msgs := []ClaudeMessage{
		{Role: "user", Content: "start the long task"},
	}
	for i := 0; i < 800; i++ {
		msgs = append(msgs,
			ClaudeMessage{Role: "assistant", Content: "step result: " + big},
			ClaudeMessage{Role: "user", Content: "next: " + big},
		)
	}
	msgs = append(msgs, ClaudeMessage{Role: "user", Content: "FINAL: summarize everything above"})

	req := &ClaudeRequest{
		Model:    "claude-opus-4.8",
		System:   "You are a helpful assistant.",
		Messages: msgs,
	}

	payload := ClaudeToKiro(req, false)

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	limit := getMaxPayloadBytes()
	if len(raw) > limit {
		t.Fatalf("payload size %d exceeds limit %d after truncation", len(raw), limit)
	}

	// The current message must be preserved.
	cur := payload.ConversationState.CurrentMessage.UserInputMessage
	if !strings.Contains(cur.Content, "FINAL: summarize everything above") {
		t.Fatalf("current message lost after truncation, got %q", cur.Content[:min(80, len(cur.Content))])
	}

	// A truncation placeholder must be present in history.
	foundPlaceholder := false
	for _, h := range payload.ConversationState.History {
		if h.UserInputMessage != nil && strings.Contains(h.UserInputMessage.Content, "truncated to fit") {
			foundPlaceholder = true
			break
		}
	}
	if !foundPlaceholder {
		t.Fatalf("expected a truncation placeholder in history")
	}

	// System priming should still be at the front.
	if len(payload.ConversationState.History) < 2 {
		t.Fatalf("expected priming retained, history too short")
	}
	primingUser := payload.ConversationState.History[0].UserInputMessage
	if primingUser == nil || !strings.Contains(primingUser.Content, "helpful assistant") {
		t.Fatalf("expected system priming retained at front")
	}
}

// TestClaudeToKiroSmallPayloadNotTruncated ensures normal-sized conversations
// are left untouched (no placeholder inserted).
func TestClaudeToKiroSmallPayloadNotTruncated(t *testing.T) {
	req := &ClaudeRequest{
		Model:  "claude-opus-4.8",
		System: "You are helpful.",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
			{Role: "user", Content: "how are you?"},
		},
	}
	payload := ClaudeToKiro(req, false)
	for _, h := range payload.ConversationState.History {
		if h.UserInputMessage != nil && strings.Contains(h.UserInputMessage.Content, "truncated to fit") {
			t.Fatalf("small payload should not be truncated")
		}
	}
}

// TestTruncatePayloadToLimitBytesHonorsCustomBudget verifies that a tighter
// admin-configured budget drops more oldest history while keeping the current
// message and a truncation placeholder.
func TestTruncatePayloadToLimitBytesHonorsCustomBudget(t *testing.T) {
	big := strings.Repeat("lorem ipsum dolor sit amet ", 40) // ~1KB
	msgs := []ClaudeMessage{
		{Role: "user", Content: "start"},
	}
	for i := 0; i < 200; i++ {
		msgs = append(msgs,
			ClaudeMessage{Role: "assistant", Content: "step: " + big},
			ClaudeMessage{Role: "user", Content: "next: " + big},
		)
	}
	msgs = append(msgs, ClaudeMessage{Role: "user", Content: "FINAL current turn"})

	req := &ClaudeRequest{
		Model:    "claude-opus-4.8",
		System:   "You are a helpful assistant.",
		Messages: msgs,
	}
	payload := ClaudeToKiro(req, false)

	// Force a much tighter budget than the default.
	const tight = 80 * 1024
	before := payloadByteSize(payload)
	if before <= tight {
		t.Fatalf("precondition failed: payload %d already under tight budget %d", before, tight)
	}
	truncatePayloadToLimitBytes(payload, true, tight)
	after := payloadByteSize(payload)
	if after > tight {
		t.Fatalf("after custom trim size %d exceeds budget %d", after, tight)
	}
	cur := payload.ConversationState.CurrentMessage.UserInputMessage.Content
	if !strings.Contains(cur, "FINAL current turn") {
		t.Fatalf("current message lost after custom-budget trim: %q", cur[:min(80, len(cur))])
	}
	found := false
	for _, h := range payload.ConversationState.History {
		if h.UserInputMessage != nil && strings.Contains(h.UserInputMessage.Content, "truncated to fit") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected truncation placeholder after custom-budget trim")
	}
}

func TestIsContentLengthErrorMessage(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{`{"message":"Input is too long.","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`, true},
		{"Context window is full. Reduce conversation history, system prompt, or tools.", true},
		{"context_length_exceeded", true},
		{"This model's maximum context length is 200000 tokens", true},
		{"rate limited (HTTP 429)", false},
		{"HTTP 401 unauthorized", false},
	}
	for _, c := range cases {
		if got := isContentLengthErrorMessage(c.msg); got != c.want {
			t.Errorf("isContentLengthErrorMessage(%q) = %v, want %v", c.msg, got, c.want)
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
