package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func bodyStr(t *testing.T, b []byte) string {
	t.Helper()
	return string(b)
}

func TestPrepareIntlBody_DeveloperRoleBecomesSystem(t *testing.T) {
	in := mustMarshal(t, map[string]any{
		"model":    "default-model",
		"messages": []any{map[string]any{"role": "developer", "content": "x"}},
	})
	out := bodyStr(t, prepareIntlBody(in))
	if strings.Contains(out, `"developer"`) {
		t.Fatalf("developer role survived: %s", out)
	}
	if !strings.Contains(out, `"role":"system"`) && !strings.Contains(out, `"role": "system"`) {
		t.Fatalf("developer not rewritten to system: %s", out)
	}
}

func TestPrepareIntlBody_ForcesStream(t *testing.T) {
	in := mustMarshal(t, map[string]any{
		"model":    "default-model",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	})
	out := bodyStr(t, prepareIntlBody(in))
	if !strings.Contains(out, `"stream":true`) {
		t.Fatalf("stream not forced: %s", out)
	}
}

func TestPrepareIntlBody_DropsOnlyOffEffort(t *testing.T) {
	off := bodyStr(t, prepareIntlBody(mustMarshal(t, map[string]any{
		"messages":         []any{map[string]any{"role": "system", "content": "s"}},
		"reasoning_effort": "off",
	})))
	if strings.Contains(off, "reasoning_effort") {
		t.Fatalf("reasoning_effort off not dropped: %s", off)
	}
	high := bodyStr(t, prepareIntlBody(mustMarshal(t, map[string]any{
		"messages":         []any{map[string]any{"role": "system", "content": "s"}},
		"reasoning_effort": "high",
	})))
	if !strings.Contains(high, `"reasoning_effort":"high"`) && !strings.Contains(high, `"reasoning_effort": "high"`) {
		t.Fatalf("declared effort was dropped: %s", high)
	}
}

func TestPrepareIntlBody_SystemFirstPrepend(t *testing.T) {
	// First message not system -> a minimal system prompt is prepended.
	user := bodyStr(t, prepareIntlBody(mustMarshal(t, map[string]any{
		"model":    "default-model",
		"messages": []any{map[string]any{"role": "user", "content": "Say OK"}},
	})))
	if strings.HasPrefix(user, `{"messages":[{"role":"user"`) {
		t.Fatalf("first message should be system: %s", user)
	}
	// First message already system -> untouched ordering, no extra message.
	system := bodyStr(t, prepareIntlBody(mustMarshal(t, map[string]any{
		"model":    "default-model",
		"messages": []any{map[string]any{"role": "system", "content": "be assistant"}, map[string]any{"role": "user", "content": "hi"}},
	})))
	if strings.Count(system, `"role":"system"`) != 1 && strings.Count(system, `"role": "system"`) != 1 {
		t.Fatalf("system-first body gained an extra system message: %s", system)
	}
}

func TestPrepareIntlBody_NonJSONPassThrough(t *testing.T) {
	raw := []byte("not json at all")
	if out := prepareIntlBody(raw); string(out) != string(raw) {
		t.Fatalf("non-JSON body was rewritten: %s", out)
	}
}

func TestNormalizeToolChoice(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"string none removes choice and tools", `{"tool_choice":"none","tools":[],"messages":[]}`, `{"messages":[]}`},
		{"object none removes choice and tools", `{"tool_choice":{"type":"none"},"tools":[],"messages":[]}`, `{"messages":[]}`},
		{"object function takes name", `{"tool_choice":{"type":"function","function":{"name":"read_file"}},"messages":[]}`, `"tool_choice":"read_file"`},
		{"auto stays string", `{"tool_choice":"auto","messages":[]}`, `"tool_choice":"auto"`},
		{"object required becomes string", `{"tool_choice":{"type":"required"},"messages":[]}`, `"tool_choice":"required"`},
	}
	for _, c := range cases {
		var obj map[string]any
		if err := json.Unmarshal([]byte(c.in), &obj); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		normalizeToolChoice(obj)
		out := bodyStr(t, mustMarshal(t, obj))
		if c.want == "remove" {
			if strings.Contains(out, "tool_choice") || strings.Contains(out, "tools") {
				t.Fatalf("%s: expected removal, got %s", c.name, out)
			}
			continue
		}
		if !strings.Contains(out, c.want) {
			t.Fatalf("%s: want %q in %s", c.name, c.want, out)
		}
	}
}

func TestRegionOf(t *testing.T) {
	if !isGlobal("www.workbuddy.ai") {
		t.Fatal("www.workbuddy.ai should be global")
	}
	if !isGlobal("WORKBUDDY.AI") {
		t.Fatal("case-insensitive match expected")
	}
	if isGlobal("copilot.tencent.com") || isGlobal("") || isGlobal("www.codebuddy.cn") {
		t.Fatal("non-international domains must not be global")
	}
}

func TestParseCredential(t *testing.T) {
	raw := `{"provider":"workbuddy","type":"workbuddy","account":{"uid":"u1","enterpriseId":"","nickname":"n1"},` +
		`"auth":{"accessToken":"at","refreshToken":"rt","expiresAt":1820968313,"domain":"www.workbuddy.ai","region":"global","loginPlatform":"CLI"}}`
	c, err := parseCredential([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.AccessToken != "at" || c.RefreshToken != "rt" || c.UID != "u1" || c.Domain != "www.workbuddy.ai" {
		t.Fatalf("credential fields wrong: %+v", c)
	}
	if c.ExpiresAtMS != 1820968313 {
		t.Fatalf("expiresAt wrong: %d", c.ExpiresAtMS)
	}
	if _, err := parseCredential([]byte(raw + `"`)); err == nil {
		t.Fatal("invalid JSON should error")
	}
	cn := `{"provider":"workbuddy","account":{"uid":"u"},"auth":{"accessToken":"at","domain":"copilot.tencent.com"}}`
	if _, err := parseCredential([]byte(cn)); err == nil {
		t.Fatal("a CN-domain credential must be rejected by the intl-only provider")
	}
}

func TestChatHeaders_CarryIdentity(t *testing.T) {
	c := &credential{AccessToken: "tok", UID: "u", EnterpriseID: "e", Domain: "www.workbuddy.ai"}
	h := chatHeaders(c, appUserAgent(appVersionFallback))
	if h["Authorization"] != "Bearer tok" {
		t.Fatalf("authorization: %q", h["Authorization"])
	}
	if h["X-User-Id"] != "u" || h["X-Enterprise-Id"] != "e" || h["X-Domain"] != "www.workbuddy.ai" {
		t.Fatalf("identity headers wrong: %+v", h)
	}
	if h["X-Product"] != "SaaS" {
		t.Fatalf("product header wrong: %+v", h)
	}
	if !strings.HasPrefix(h["User-Agent"], "WorkBuddy/5.5.2 WorkBuddy AI/5.5.2") {
		t.Fatalf("app UA wrong: %q", h["User-Agent"])
	}
}

func TestRefreshHeaders_NoAuthorization(t *testing.T) {
	c := &credential{RefreshToken: "rt", EnterpriseID: "e"}
	h := refreshHeaders(c)
	if _, has := h["Authorization"]; has {
		t.Fatal("refresh must not carry Authorization")
	}
	if h["X-Refresh-Token"] != "rt" || h["X-Auth-Refresh-Source"] != "workbuddy" {
		t.Fatalf("refresh headers wrong: %+v", h)
	}
}