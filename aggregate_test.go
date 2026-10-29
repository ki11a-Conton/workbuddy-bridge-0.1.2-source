// aggregate_test.go — the non-stream executor folds an OpenAI-shaped SSE into a
// single chat.completion. Upstream repeats `"role":"assistant"` in every delta
// frame, so role is a single-valued field here: taking each occurrence must not
// accumulate ("assistantassistantassistant").
package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAggregateCompletionRoleNotConcatenated(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"c1","model":"deepseek-v4.1-flash","created":1,"choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"deepseek-v4.1-flash","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"he"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"deepseek-v4.1-flash","created":1,"choices":[{"index":0,"delta":{"role":"assistant","content":"llo"},"finish_reason":null}]}`,
		`data: {"id":"c1","model":"deepseek-v4.1-flash","created":1,"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n")

	out, err := aggregateCompletion(strings.NewReader(sse), "deepseek-v4.1-flash")
	if err != nil {
		t.Fatalf("aggregateCompletion: %v", err)
	}
	var got struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, out)
	}
	if len(got.Choices) != 1 {
		t.Fatalf("choices = %d, want 1 (%s)", len(got.Choices), out)
	}
	if r := got.Choices[0].Message.Role; r != "assistant" {
		t.Fatalf("role = %q, want assistant", r)
	}
	if c := got.Choices[0].Message.Content; c != "hello" {
		t.Fatalf("content = %q, want hello", c)
	}
	if got.Choices[0].FinishReason != "stop" {
		t.Fatalf("finish_reason = %q, want stop", got.Choices[0].FinishReason)
	}
}

// A stream with no role frame at all still has to yield the OpenAI default.
func TestAggregateCompletionRoleDefaultsToAssistant(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"id":"c2","model":"m","created":1,"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}, "\n")

	out, err := aggregateCompletion(strings.NewReader(sse), "m")
	if err != nil {
		t.Fatalf("aggregateCompletion: %v", err)
	}
	if !strings.Contains(string(out), `"role":"assistant"`) {
		t.Fatalf("missing default assistant role in %s", out)
	}
}
