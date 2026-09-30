// wire.go — the international WorkBuddy wire, ported from
// zlZayn/dsh-workbuddy-bridge (region=global / https://www.workbuddy.ai) and
// verified live by D:\toolchain\spike-workbuddy-ai (HTTP 200 on both
// /v3/config and /v2/chat/completions with OpenAI-shaped SSE).
//
// This file is pure: no C ABI, no host calls — everything here is unit-testable
// and independent of the CPA plugin runtime.
package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	globalBase         = "https://www.workbuddy.ai"
	chatEndpoint       = "/v2/chat/completions"
	refreshEndpoint    = "/v2/plugin/auth/token/refresh"
	catalogEndpoint    = "/v3/config"
	billingEndpoint    = "/v2/billing/meter/get-user-resource"
	appVersionFallback = "5.5.2"
	clientUA           = "CLI/2.63.2 CodeBuddy/2.63.2"
	systemPrompt       = "You are a helpful assistant."
	errorBodyLimit     = 4096
)

// credential is the provider-owned portion of one CPA auth record for the
// international product. It maps from the physical CPA auth-file shape written
// by the cpa-multi-plugins workbuddy plugin (our source of global accounts).
type credential struct {
	AccessToken        string
	RefreshToken       string
	ExpiresAtMS        int64
	RefreshExpiresAtMS int64
	Domain             string
	Region             string
	LoginPlatform      string
	UID                string
	EnterpriseID       string
	Nickname           string
}

// cpaAuthFile mirrors the physical CPA auth-file layout (top-level keys
// provider/type/account/auth) so a global WorkBuddy account can be adopted
// verbatim into the workbuddy-ai provider.
type cpaAuthFile struct {
	Provider string `json:"provider"`
	Type     string `json:"type"`
	Account  struct {
		UID          string `json:"uid"`
		EnterpriseID string `json:"enterpriseId"`
		Nickname     string `json:"nickname"`
	} `json:"account"`
	Auth struct {
		AccessToken      string `json:"accessToken"`
		RefreshToken     string `json:"refreshToken"`
		ExpiresAt        int64  `json:"expiresAt"`
		RefreshExpiresAt int64  `json:"refreshExpiresAt"`
		Domain           string `json:"domain"`
		Region           string `json:"region"`
		LoginPlatform    string `json:"loginPlatform"`
	} `json:"auth"`
}

// parseCredential reads one CPA auth file and rejects anything that is not the
// international product (domain must be workbuddy.ai).
func parseCredential(raw []byte) (*credential, error) {
	var f cpaAuthFile
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse auth file: %w", err)
	}
	if f.Auth.AccessToken == "" {
		return nil, fmt.Errorf("auth file has no accessToken")
	}
	c := &credential{
		AccessToken:        f.Auth.AccessToken,
		RefreshToken:       f.Auth.RefreshToken,
		ExpiresAtMS:        f.Auth.ExpiresAt,
		RefreshExpiresAtMS: f.Auth.RefreshExpiresAt,
		Domain:             f.Auth.Domain,
		Region:             f.Auth.Region,
		LoginPlatform:      f.Auth.LoginPlatform,
		UID:                f.Account.UID,
		EnterpriseID:       f.Account.EnterpriseID,
		Nickname:           f.Account.Nickname,
	}
	if !isGlobal(f.Auth.Domain) {
		return nil, fmt.Errorf("credential domain %q is not international (www.workbuddy.ai)", f.Auth.Domain)
	}
	return c, nil
}

// isGlobal mirrors the bridge's regionOf: only workbuddy.ai counts as the
// international product; anything else (including empty) is CN and out of scope.
func isGlobal(domain string) bool {
	d := strings.ToLower(strings.TrimSpace(domain))
	return d == "workbuddy.ai" || strings.HasSuffix(d, ".workbuddy.ai")
}

// appUserAgent is the desktop-shaped UA the international chat and catalog
// requests present as; the gateway splits the /v3/config roster by it.
func appUserAgent(version string) string {
	return "WorkBuddy/" + version + " WorkBuddy AI/" + version
}

func commonHeaders() map[string]string {
	return map[string]string{
		"Accept":           "application/json, text/plain, */*",
		"X-Requested-With": "XMLHttpRequest",
		"Origin":           globalBase,
		"Referer":          globalBase + "/",
		"User-Agent":       clientUA,
	}
}

// chatHeaders builds the chat request headers. userAgent carries the desktop
// identity; Authorization carries the access token (never the refresh token).
func chatHeaders(c *credential, userAgent string) map[string]string {
	h := commonHeaders()
	h["User-Agent"] = userAgent
	h["Content-Type"] = "application/json"
	if c.UID == "" {
		h["X-No-User-Id"] = "1"
	} else {
		h["X-User-Id"] = c.UID
	}
	if c.EnterpriseID == "" {
		h["X-No-Enterprise-Id"] = "1"
	} else {
		h["X-Enterprise-Id"] = c.EnterpriseID
	}
	if c.Domain == "" {
		h["X-No-Department-Info"] = "1"
	} else {
		h["X-Domain"] = c.Domain
	}
	h["X-Product"] = "SaaS"
	h["Authorization"] = "Bearer " + c.AccessToken
	return h
}

// refreshHeaders carries the refresh token in a header and nowhere else; no
// Authorization header on this path, mirroring the bridge.
func refreshHeaders(c *credential) map[string]string {
	h := commonHeaders()
	h["X-Refresh-Token"] = c.RefreshToken
	h["X-Auth-Refresh-Source"] = "workbuddy"
	if c.EnterpriseID != "" {
		h["X-Enterprise-Id"] = c.EnterpriseID
	}
	return h
}

// billingHeaders mirror the bridge's billing request headers.
func billingHeaders(c *credential) map[string]string {
	h := map[string]string{
		"Authorization": "Bearer " + c.AccessToken,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}
	if c.UID != "" {
		h["X-User-Id"] = c.UID
	}
	if c.EnterpriseID != "" {
		h["X-Enterprise-Id"] = c.EnterpriseID
		h["X-Tenant-Id"] = c.EnterpriseID
	}
	if c.Domain != "" {
		h["X-Domain"] = c.Domain
	}
	return h
}

// catalogHeaders for the international /v3/config: app-shaped UA plus the
// SaaS identity headers the product document answers to.
func catalogHeaders(c *credential, version string) map[string]string {
	h := map[string]string{
		"Authorization":    "Bearer " + c.AccessToken,
		"Accept":           "application/json",
		"Origin":           globalBase,
		"Referer":          globalBase + "/",
		"X-Requested-With": "XMLHttpRequest",
		"X-Product":        "SaaS",
		"User-Agent":       appUserAgent(version),
	}
	return h
}

// ---------------------------------------------------------------------------
// body preparation (ported rules, unit-tested)
// ---------------------------------------------------------------------------

// prepareChatBody normalizes an OpenAI chat body for the WorkBuddy upstream:
// force stream:true (the upstream rejects non-streaming), rewrite
// role:developer to role:system (upstream rejects developer with 400/11128 on
// CN; harmless here), and flatten tool_choice into the upstream's string form.
func prepareChatBody(raw []byte) []byte {
	obj, ok := parseObjectBody(raw)
	if !ok {
		return raw
	}
	obj["stream"] = true
	normalizeDeveloperRole(obj)
	normalizeToolChoice(obj)
	return mustJSON(obj)
}

// prepareIntlBody applies the international-only rules on top of
// prepareChatBody: drop the adapter's own reasoning_effort "off" spelling
// (the global endpoint rejects it on the GPT family with 400/11133
// reasoning.effort), and prepend a minimal system message when the first
// message is not one (the global gateway rejects such bodies with 400/11128
// "first message is not system prompt").
func prepareIntlBody(raw []byte) []byte {
	prepared := prepareChatBody(raw)
	obj, ok := parseObjectBody(prepared)
	if !ok {
		return prepared
	}
	dropUnsupportedEffort(obj)
	msgs, ok := obj["messages"].([]any)
	if !ok || len(msgs) == 0 {
		return mustJSON(obj)
	}
	if first, ok := msgs[0].(map[string]any); ok && first["role"] == "system" {
		return mustJSON(obj)
	}
	msgs = append([]any{map[string]any{"role": "system", "content": systemPrompt}}, msgs...)
	obj["messages"] = msgs
	return mustJSON(obj)
}

func parseObjectBody(raw []byte) (map[string]any, bool) {
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, false
	}
	return obj, true
}

func mustJSON(obj map[string]any) []byte {
	out, err := json.Marshal(obj)
	if err != nil {
		return nil
	}
	return out
}

func normalizeDeveloperRole(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		wrapped, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if wrapped["role"] == "developer" {
			wrapped["role"] = "system"
		}
	}
}

// normalizeToolChoice rewrites OpenAI tool_choice spellings into the upstream's
// string form; an object with type "none" suppresses the tool list entirely.
func normalizeToolChoice(obj map[string]any) {
	suppress := func() {
		delete(obj, "tools")
		delete(obj, "functions")
	}
	choice, present := obj["tool_choice"]
	if !present {
		return
	}
	switch v := choice.(type) {
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "none":
			delete(obj, "tool_choice")
			suppress()
		}
	case map[string]any:
		t, _ := v["type"].(string)
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "none":
			delete(obj, "tool_choice")
			suppress()
		case "auto", "required":
			obj["tool_choice"] = strings.ToLower(strings.TrimSpace(t))
		case "function":
			var name string
			if fn, ok := v["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
			if name == "" {
				name, _ = v["name"].(string)
			}
			name = strings.TrimSpace(name)
			if name != "" {
				obj["tool_choice"] = name
			} else {
				obj["tool_choice"] = "auto"
			}
		default:
			delete(obj, "tool_choice")
		}
	default:
		delete(obj, "tool_choice")
	}
}

// dropUnsupportedEffort: the global endpoint rejects the literal "off" on the
// GPT family (400/11133, extError.param == reasoning.effort). Omission is the
// only form measured good on every such model; declared spellings
// (low/medium/high/xhigh/max) and an explicit none pass through untouched.
func dropUnsupportedEffort(obj map[string]any) {
	if obj["reasoning_effort"] == "off" {
		delete(obj, "reasoning_effort")
	}
}
