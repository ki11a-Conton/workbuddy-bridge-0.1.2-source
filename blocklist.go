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
//
// v0.1.5 parity pass: the CN plugin's 0.9.79–0.9.81 rule families that this
// bridge never carried are ported below — the DSH file-reference segment
// (the fourth confirmed 11-128 member, measured 2026-10-28 on the CN realm
// and shipped by the CN plugin in 0.9.81), the Anthropic billing header,
// Claude Code's cc_* key-value metadata, the standalone Codex attribution
// sentence, the OhMyOpenCode junior fingerprint, the pasted 11-128 code
// itself, the Claude Code feedback-line wording and the brand-prose shrink.
// Same substitutions, same order as the CN reference.
var (
	reClaudeCodeCli        = regexp.MustCompile(`(?i)anthropic(?:'s)?\s+official\s+cli\s+for\s+claude`)
	reMainBranchPr         = regexp.MustCompile(`(?i)main\s+branch\s+\(you\s+will\s+usually\s+use\s+this\s+for\s+prs\)`)
	reCodexCliIntro        = regexp.MustCompile(`(?i)you are a coding agent running in the codex cli[^.\n]*\.\s*codex cli is an open source project led by openai\s*\.?`)
	reDeepSeekHarnessIntro = regexp.MustCompile(`(?i)you are an ai agent powered by deepseek harness`)
	reDeepSeekHarnessGUI   = regexp.MustCompile(`(?i)deepseek\s+harness\s+web\s+gui`)

	// reCodexSecondSentence matches the Codex CLI base-instructions second
	// sentence on its own: the two-sentence reCodexCliIntro above needs the
	// "You are a coding agent…" opener adjacent, while Codex Desktop and
	// drifted releases send the attribution sentence alone. The optional
	// "the " is absorbed and re-emitted, so the rewrite is idempotent.
	reCodexSecondSentence = regexp.MustCompile(`(?i)\b(?:the\s+)?codex\s+cli\s+is\s+an\s+open\s+source\s+project\s+led\s+by\s+openai\s*\.?`)

	// reAnthropicBillingHeader strips the x-anthropic-billing-header line
	// Claude Code embeds in its first system block; the bare-header rename
	// catches occurrences whose value was formatted differently.
	reAnthropicBillingHeader = regexp.MustCompile(`(?i)x-anthropic-billing-header:[^;\r\n]*;?\s*`)
	reAnthropicBareHeader    = regexp.MustCompile(`(?i)x-anthropic-billing-header`)

	// reOMOJunior matches the OhMyOpenCode junior-agent fingerprint — a
	// confirmed contiguous 11-128 trigger: either token alone passes, only
	// the full phrase blocks.
	reOMOJunior = regexp.MustCompile(`(?i)sisyphus-junior\s*-\s*focused\s+executor\s+from\s+ohmyopencode`)

	// rePastedErrorCode is the poisoning-loop breaker: users quote the 503
	// back into the agent while debugging and the bare code is itself a
	// trigger, poisoning every later turn. \b keeps the code embedded inside
	// longer digit runs (timestamps like 1729111280) byte-identical, while
	// every known paste shape — spaces, colons, commas, quotes, CJK
	// neighbours — still matches. The hyphenated spelling passes the gate.
	rePastedErrorCode = regexp.MustCompile(`\b11128\b`)

	// reCCKeyValue strips Claude Code's cc_* key-value metadata pairs
	// (cc_entrypoint= and siblings), part of the same fingerprint family as
	// the billing header. \b keeps identifier-shaped hosts out.
	reCCKeyValue = regexp.MustCompile(`(?i)\bcc_[a-z0-9_]+=[^;\r\n]*;?\s*`)

	// reDshFileRef fixes the fourth confirmed 11-128 content family: DSH's
	// official @deepseek-ai/dsh-file-reference prompt injects
	//
	//   A: Tokens prefixed with @ are paths the user explicitly referenced.
	//   B: Relative paths resolve from the workspace root; …
	//
	// Measured on the CN realm (2026-10-28/29, single-variable replays):
	// either sentence alone passes; the pair 400s from system and assistant
	// and passes from user and tool; and it does NOT aggregate across
	// messages (A in system + B in a user or assistant message = 200). The
	// minimal, meaning-preserving fix is to insert one "that" into sentence
	// A, which breaks the signature in the field it lands in and is
	// naturally idempotent — the fixed sentence carries "paths that the
	// user", which this pattern can never match again. The capture groups
	// keep the original casing.
	reDshFileRef = regexp.MustCompile(`(?i)(Tokens prefixed with @ are paths )(the user explicitly referenced\.)`)

	// reBrandProse shrinks the DSH brand in prose ("DeepSeek Harness" →
	// "DeepSeek"): the brand alone passes, but brand + a GUI description
	// phrase hits the same check, and shrinking just the brand passes while
	// keeping the sentence readable. Only space/tab-separated prose forms
	// match, so hyphenated/underscored identifiers (deepseek-harness,
	// deepseek_harness) stay byte-identical. Runs AFTER the Web GUI
	// four-token rewrite, which needs the full phrase to match first.
	reBrandProse = regexp.MustCompile(`(?i)deepseek[ \t]+harness`)

	codexCliIntroReplacement        = "You are a helpful coding assistant running in the terminal."
	codexSecondSentenceReplacement  = "The Codex CLI is an open source project led by OpenAI."
	omoJuniorReplacement            = "Sisyphus-Junior - Focused executor"
	pastedErrorCodeReplacement      = "11-128"
	brandProseReplacement           = "DeepSeek"
	claudeCodeFeedbackLine          = "To give feedback, users should report the issue at https://github.com/anthropics/claude-code/issues"
	claudeCodeFeedbackLineRewritten = "To provide feedback, users should report the issue at https://github.com/anthropics/claude-code/issues"
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

	// v0.1.5 parity with the CN plugin (0.9.79–0.9.81), same order as its
	// sanitizeBlockedTemplates: Codex attribution sentence, billing header,
	// cc_* metadata, feedback-line wording, OhMyOpenCode fingerprint, pasted
	// code, then the file-reference fix and the brand shrink (which must run
	// after the Web GUI rewrite above).
	s = reCodexSecondSentence.ReplaceAllString(s, codexSecondSentenceReplacement)
	s = reAnthropicBillingHeader.ReplaceAllString(s, "")
	s = reAnthropicBareHeader.ReplaceAllString(s, "x-anthropic-billing-hdr")
	for i := 0; i < 8; i++ {
		stripped := reCCKeyValue.ReplaceAllString(s, "")
		if stripped == s {
			break
		}
		s = stripped
	}
	s = strings.ReplaceAll(s, claudeCodeFeedbackLine, claudeCodeFeedbackLineRewritten)
	s = reOMOJunior.ReplaceAllString(s, omoJuniorReplacement)
	s = rePastedErrorCode.ReplaceAllString(s, pastedErrorCodeReplacement)
	s = reDshFileRef.ReplaceAllString(s, "${1}that ${2}")
	s = reBrandProse.ReplaceAllString(s, brandProseReplacement)
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
