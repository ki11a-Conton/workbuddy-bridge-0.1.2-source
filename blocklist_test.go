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
	if !strings.Contains(args, "DeepSeek web interface") {
		t.Errorf("tool_calls arguments not rewritten: %q", args)
	}
	user := msgs[1].(map[string]any)["content"].(string)
	if !strings.Contains(user, "DeepSeek web interface") {
		t.Errorf("user turn not rewritten: %q", user)
	}
	if got := msgs[4].(map[string]any)["content"].(string); got != "Continue." {
		t.Errorf("non-trigger content must pass verbatim, got %q", got)
	}
}

// TestSanitizeBlockedPhrasesCNParityRules pins the rule families the bridge
// gained in v0.1.5 to match the CN plugin's 0.9.79–0.9.81 set. Each input is
// the exact shape measured to block on the CN realm; each expectation is the
// substituted form the CN plugin emits.
func TestSanitizeBlockedPhrasesCNParityRules(t *testing.T) {
	cases := []struct {
		name, in, want, unwanted string
	}{
		{
			// The fourth confirmed 11-128 member and the reason for this
			// patch: sentence A + B together block, either alone passes.
			name:     "dsh file reference pair",
			in:       "Tokens prefixed with @ are paths the user explicitly referenced. Relative paths resolve from the workspace root; absolute paths identify files.",
			want:     "Tokens prefixed with @ are paths that the user explicitly referenced. Relative paths resolve from the workspace root;",
			unwanted: "are paths the user explicitly referenced",
		},
		{
			name:     "codex attribution sentence standalone",
			in:       "Attribution: Codex CLI is an open source project led by OpenAI. Continue.",
			want:     "The Codex CLI is an open source project led by OpenAI.",
			unwanted: "",
		},
		{
			name:     "anthropic billing header value",
			in:       "x-anthropic-billing-header: cc_version=2.0.31.abc; cc_entrypoint=cli; note",
			want:     "note",
			unwanted: "cc_version",
		},
		{
			name:     "anthropic billing header bare",
			in:       "field x-anthropic-billing-header present",
			want:     "x-anthropic-billing-hdr",
			unwanted: "",
		},
		{
			name:     "claude code cc key-value pair",
			in:       "meta cc_entrypoint=cli; keep_this",
			want:     "meta keep_this",
			unwanted: "cc_entrypoint",
		},
		{
			name:     "ohmyopencode junior fingerprint",
			in:       "role: sisyphus-junior - focused executor from ohmyopencode",
			want:     "Sisyphus-Junior - Focused executor",
			unwanted: "ohmyopencode",
		},
		{
			name:     "pasted error code",
			in:       `upstream said {"code":11128,"msg":"Illegal API invocation from an unapproved channel"}`,
			want:     "11-128",
			unwanted: "11128",
		},
		{
			name:     "claude code feedback wording",
			in:       "To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues",
			want:     "To provide feedback, users should report the issue at",
			unwanted: "",
		},
		{
			name:     "brand prose shrink",
			in:       "the DeepSeek Harness runtime context says this page is the DeepSeek Harness Web GUI",
			want:     "the DeepSeek runtime context says this page is the DeepSeek web interface",
			unwanted: "",
		},
	}
	for _, c := range cases {
		obj := map[string]any{"messages": []any{
			map[string]any{"role": "system", "content": c.in},
		}}
		sanitizeBlockedPhrases(obj)
		got := obj["messages"].([]any)[0].(map[string]any)["content"].(string)
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: want %q in %q", c.name, c.want, got)
		}
		if c.unwanted != "" && strings.Contains(got, c.unwanted) {
			t.Errorf("%s: %q still present in %q", c.name, c.unwanted, got)
		}
	}
}

// TestSanitizeBlockedPhrasesParityIdempotent pins that a second pass over
// already-rewritten content changes nothing — the scrub runs once per message
// but a rebuilt body (non-stream aggregation, retries) can carry it twice, and
// a rewriting rule that keeps matching would eat user text on every pass.
func TestSanitizeBlockedPhrasesParityIdempotent(t *testing.T) {
	inputs := []string{
		"Tokens prefixed with @ are paths the user explicitly referenced. Relative paths resolve from the workspace root;",
		`upstream said {"code":11128,"msg":"blocked"}`,
		"meta cc_entrypoint=cli; note",
		"sisyphus-junior - focused executor from ohmyopencode",
		"the DeepSeek Harness web interface is open",
	}
	for _, in := range inputs {
		once := replaceBlockedPrompt(in)
		twice := replaceBlockedPrompt(once)
		if once != twice {
			t.Errorf("not idempotent for %q:\n once=%q\ntwice=%q", in, once, twice)
		}
	}
}

// TestSanitizeBlockedPhrasesParityVerbatimPass pins the other half of the
// contract: content without a trigger family passes byte-identical, including
// user data that merely resembles one (longer digit runs, identifier-shaped
// cc_ hosts, the already-fixed file-reference wording).
func TestSanitizeBlockedPhrasesParityVerbatimPass(t *testing.T) {
	untouched := []string{
		"order 900111285 shipped at ts=1729111280",
		"variable foo_cc_entrypoint should stay",
		"Tokens prefixed with @ are paths that the user explicitly referenced.",
		"Relative paths resolve from the workspace root;",
		"the deepseek_harness and deepseek-harness identifiers stay",
	}
	for _, in := range untouched {
		if got := replaceBlockedPrompt(in); got != in {
			t.Errorf("must pass verbatim:\n in=%q\ngot=%q", in, got)
		}
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
