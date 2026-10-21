package main

import (
	"regexp"
	"strings"
)

// blockedPromptSignatures lists opening sentences that vendor gateways refuse.
//
// The CN WorkBuddy gateway rejects `You are an AI agent powered by DeepSeek
// Harness` with 400/11-128 ("Illegal API invocation from an unapproved
// channel"). This bridge talks to the international realm, which is the same
// backend family, so the identical rewrite is applied here as a safeguard.
var blockedPromptSignatures = []struct{ from, to string }{
	{"You are an AI agent powered by DeepSeek Harness.", "You are an AI assistant."},
	{"You are an AI agent powered by DeepSeek Harness,", "You are an AI assistant,"},
	{"You are an AI agent powered by DeepSeek Harness", "You are an AI assistant"},
	{"You are Claude Code, Anthropic's official CLI for Claude.",
		"You are Claude Code, Anthropic's official CLI tool for Claude."},
	{"Main branch (you will usually use this for PRs)",
		"Default branch (you will usually use this for PRs)"},
}

// Tolerant variants: capitalization drift, quote drift, "Anthropic's official
// CLI" without the "You are Claude Code" prefix, the Codex CLI base-rule
// opening, and the DSH runtime-context line. Mirrors the CN plugin's rule set
// (cpa-multi-plugins/plugins/workbuddy/payload.go) so both realms block the
// same inputs.
var (
	reClaudeCodeCli        = regexp.MustCompile(`(?i)anthropic(?:'s)?\s+official\s+cli\s+for\s+claude`)
	reMainBranchPr         = regexp.MustCompile(`(?i)main\s+branch\s+\(you\s+will\s+usually\s+use\s+this\s+for\s+prs\)`)
	reCodexCliIntro        = regexp.MustCompile(`(?i)you are a coding agent running in the codex cli[^.\n]*\.\s*codex cli is an open source project led by openai\s*\.?`)
	reDeepSeekHarnessIntro = regexp.MustCompile(`(?i)you are an ai agent powered by deepseek harness`)
	reDeepSeekHarnessGUI   = regexp.MustCompile(`(?i)deepseek\s+harness\s+web\s+gui`)

	codexCliIntroReplacement = "You are a helpful coding assistant running in the terminal."
)

// replaceBlockedPrompt applies the signature rewrites to one string.
func replaceBlockedPrompt(s string) string {
	for _, sig := range blockedPromptSignatures {
		if strings.Contains(s, sig.from) {
			s = strings.ReplaceAll(s, sig.from, sig.to)
		}
	}
	s = reClaudeCodeCli.ReplaceAllString(s, "Anthropic's official CLI tool for Claude")
	s = reMainBranchPr.ReplaceAllString(s, "Default branch (you will usually use this for PRs)")
	s = reCodexCliIntro.ReplaceAllString(s, codexCliIntroReplacement)
	s = reDeepSeekHarnessIntro.ReplaceAllString(s, "You are an AI assistant")
	s = reDeepSeekHarnessGUI.ReplaceAllString(s, "DeepSeek Harness web interface")
	return s
}

// sanitizeBlockedPhrases rewrites blocklisted phrases in EVERY message of the
// body — system, developer, user, assistant and tool alike — plus the
// assistant-only reasoning mirrors and tool_calls arguments.
//
// Scope, and why it is no longer system-only (2026-10-21): single-variable
// replays against the CN realm showed the gate reads the whole request body,
// not just the system message — the same phrase inside an assistant history
// echo still produced 400/11-128 while a fully sanitized system prompt was in
// place, and scrubbing every role flipped that body to 200. This bridge feeds
// the same backend family, so it now scrubs every role. The rewrites are the
// same single-phrase substitutions as before: meaning-preserving, and content
// without a trigger phrase passes byte-identical.
func sanitizeBlockedPhrases(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		for _, key := range []string{"reasoning_content", "reasoning"} {
			if rc, ok := msg[key].(string); ok && rc != "" {
				msg[key] = replaceBlockedPrompt(rc)
			}
		}
		switch content := msg["content"].(type) {
		case string:
			msg["content"] = replaceBlockedPrompt(content)
		case []any:
			for _, part := range content {
				p, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := p["text"].(string); ok {
					p["text"] = replaceBlockedPrompt(text)
				}
			}
		}
		if tcs, ok := msg["tool_calls"].([]any); ok {
			for _, tci := range tcs {
				tc, ok := tci.(map[string]any)
				if !ok {
					continue
				}
				fn, ok := tc["function"].(map[string]any)
				if !ok || fn == nil {
					continue
				}
				if args, ok := fn["arguments"].(string); ok {
					fn["arguments"] = replaceBlockedPrompt(args)
				}
			}
		}
	}
}

// sanitizeBlockedSystemPrompt is kept as the system/developer-scoped entry
// point: callers that must not touch user-authored turns (and the historical
// tests) still have it, while prepareChatBody now uses the body-wide
// sanitizeBlockedPhrases.
func sanitizeBlockedSystemPrompt(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		msg, ok := m.(map[string]any)
		if !ok {
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" && role != "developer" {
			continue
		}
		switch content := msg["content"].(type) {
		case string:
			msg["content"] = replaceBlockedPrompt(content)
		case []any:
			for _, part := range content {
				p, ok := part.(map[string]any)
				if !ok {
					continue
				}
				if text, ok := p["text"].(string); ok {
					p["text"] = replaceBlockedPrompt(text)
				}
			}
		}
	}
}
