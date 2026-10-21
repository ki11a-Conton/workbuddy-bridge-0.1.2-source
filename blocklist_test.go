package main

import (
	"strings"
	"testing"
)

func TestSanitizeBlockedSystemPromptString(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "You are an AI agent powered by DeepSeek Harness. Follow the rules."},
		map[string]any{"role": "user", "content": "You are an AI agent powered by DeepSeek Harness."},
	}}
	sanitizeBlockedSystemPrompt(obj)
	msgs := obj["messages"].([]any)
	sys := msgs[0].(map[string]any)["content"].(string)
	if !strings.HasPrefix(sys, "You are an AI assistant. Follow the rules.") {
		t.Fatalf("system not rewritten: %q", sys)
	}
	user := msgs[1].(map[string]any)["content"].(string)
	if user != "You are an AI agent powered by DeepSeek Harness." {
		t.Fatalf("user content must stay untouched: %q", user)
	}
}

func TestSanitizeBlockedSystemPromptParts(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "developer", "content": []any{
			map[string]any{"type": "text", "text": "You are an AI agent powered by DeepSeek Harness"},
		}},
	}}
	sanitizeBlockedSystemPrompt(obj)
	parts := obj["messages"].([]any)[0].(map[string]any)["content"].([]any)
	got := parts[0].(map[string]any)["text"].(string)
	if got != "You are an AI assistant" {
		t.Fatalf("part not rewritten: %q", got)
	}
}

func TestSanitizeBlockedSystemPromptNoop(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "You are a helpful assistant."},
	}}
	sanitizeBlockedSystemPrompt(obj)
	if got := obj["messages"].([]any)[0].(map[string]any)["content"].(string); got != "You are a helpful assistant." {
		t.Fatalf("unexpected rewrite: %q", got)
	}
}

// TestSanitizeBlockedPhrasesEveryRole pins the 2026-10-21 field failure
// (CN realm, code 11128): the gateway reads the whole request body, so an
// assistant history echo, a tool_calls argument or a reasoning mirror can
// re-arm a blocked phrase even with a clean system prompt.
func TestSanitizeBlockedPhrasesEveryRole(t *testing.T) {
	obj := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "You are an AI assistant."},
		map[string]any{"role": "user", "content": "context: the DeepSeek Harness Web GUI is open."},
		map[string]any{
			"role":              "assistant",
			"content":           "summary:\n- the DeepSeek Harness Web GUI\n- Main branch (you will usually use this for PRs)\nend",
			"reasoning_content": "recall the DeepSeek Harness Web GUI line",
			"reasoning":         "Main branch (you will usually use this for PRs)",
			"tool_calls": []any{map[string]any{
				"id": "call_1", "type": "function",
				"function": map[string]any{
					"name":      "read_file",
					"arguments": `{"note":"saw the DeepSeek Harness Web GUI"}`,
				},
			}},
		},
		map[string]any{"role": "tool", "tool_call_id": "call_1", "content": "ok"},
		map[string]any{"role": "user", "content": "Continue."},
	}}
	sanitizeBlockedPhrases(obj)
	msgs := obj["messages"].([]any)
	asst := msgs[2].(map[string]any)
	for _, key := range []string{"content", "reasoning_content", "reasoning"} {
		got, _ := asst[key].(string)
		lower := strings.ToLower(got)
		for _, bad := range []string{"deepseek harness web gui", "main branch (you will usually use this for prs)"} {
			if strings.Contains(lower, bad) {
				t.Errorf("%s still carries %q: %q", key, bad, got)
			}
		}
		if got == "" {
			t.Errorf("%s must keep its content, got empty", key)
		}
	}
	tcs := asst["tool_calls"].([]any)
	args := tcs[0].(map[string]any)["function"].(map[string]any)["arguments"].(string)
	if !strings.Contains(args, "DeepSeek Harness web interface") {
		t.Errorf("tool_calls arguments not rewritten: %q", args)
	}
	user := msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "DeepSeek Harness web interface") {
		t.Errorf("user turn not rewritten: %q", user)
	}
	if got := msgs[4].(map[string]any)["content"].(string); got != "Continue." {
		t.Errorf("non-trigger content must pass verbatim, got %q", got)
	}
}

// TestSanitizeBlockedPhrasesVariantRules covers the four tolerant rules that
// the CN plugin also carries (Claude Code CLI phrasing, Codex CLI intro,
// DeepSeek Harness intro) so both realms block the same inputs.
func TestSanitizeBlockedPhrasesVariantRules(t *testing.T) {
	cases := []struct{ in, bad string }{
		{"x Anthropic's Official CLI for Claude y", "official cli for claude"},
		{"You are a coding agent running in the Codex CLI, a terminal-based coding assistant. Codex CLI is an open source project led by OpenAI. Do the task.", "codex cli is an open source project"},
		{"you are an AI agent powered by DeepSeek Harness, act.", "powered by deepseek harness"},
	}
	for _, c := range cases {
		obj := map[string]any{"messages": []any{
			map[string]any{"role": "assistant", "content": c.in},
		}}
		sanitizeBlockedPhrases(obj)
		got := obj["messages"].([]any)[0].(map[string]any)["content"].(string)
		if strings.Contains(strings.ToLower(got), c.bad) {
			t.Errorf("rule for %q left %q in %q", c.bad, c.bad, got)
		}
	}
}
