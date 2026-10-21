package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// statusError carries an upstream HTTP status across the RPC boundary. The
// host's envelope decode rebuilds it as an rpcError whose StatusCode() drives
// MarkResult's per-status credential cooldown: 402 -> 30 min, 429 -> escalating
// quota backoff, 401 -> 30 min. Request-level failures must NOT be wrapped this
// way — they stay plain errors with status 0 and only earn the host's
// 1-minute transient cooldown.
type statusError struct {
	status int
	err    error
}

func (e *statusError) Error() string   { return e.err.Error() }
func (e *statusError) StatusCode() int { return e.status }
func (e *statusError) Unwrap() error   { return e.err }

// upstreamStatusError wraps an upstream chat failure with the HTTP status the
// host cooldown layer should attribute to the credential. Mirrors the CN
// plugin's policy (cpa-multi-plugins/plugins/workbuddy/chat_error.go:156) so
// both realms fail the same way.
//
// Passed through (account-level): 401 dead token, 402 payment/credits, 429
// rate/quota, and business 403s that carry an upstream envelope.
//
// Kept at status 0 (request/IP-level, credential stays healthy): 413/11115
// prompt overflow, 11102 model-catalog rejection, the 6004 model-scoped
// frequency limit, bare 403 WAF challenges, and everything else.
func upstreamStatusError(status int, payload string, err error) error {
	switch {
	// 11128 is request-shaped, yet a bare error made the host read it as a
	// credential failure: it walked every account, cooled all of them, and the
	// next calls died with "auth_unavailable" (503) although the credentials
	// were fine (measured 2026-10-21 on this bridge: 400/11128 followed
	// immediately by 503 auth_unavailable). Attaching 400 makes the host
	// classify it request-scoped (clienterror.IsRequestFault) and stop after
	// the first rejection.
	case isChannelRiskControl(status, payload):
		return &statusError{status: http.StatusBadRequest, err: err}
	case isPromptTooLong(status, payload),
		isModelNotRegistered(payload),
		isModelScopedRateLimit(payload),
		isWafBlocked(status, payload):
		return err
	case status == http.StatusUnauthorized,
		status == http.StatusPaymentRequired,
		status == http.StatusTooManyRequests:
		return &statusError{status: status, err: err}
	case status == http.StatusForbidden && hasBusinessEnvelope(payload):
		return &statusError{status: status, err: err}
	}
	return err
}

// isChannelRiskControl reports whether an upstream rejection is the channel
// risk-control family ("Illegal API invocation from an unapproved channel"),
// which is request-shaped and never account-level.
func isChannelRiskControl(status int, payload string) bool {
	if status != http.StatusBadRequest && status != http.StatusForbidden {
		return false
	}
	var shape struct {
		Code int `json:"code"`
	}
	if err := json.Unmarshal([]byte(payload), &shape); err == nil && shape.Code == 11128 {
		return true
	}
	// Envelope-variant tolerance: unparsable JSON that still spells the code.
	low := strings.ToLower(payload)
	return strings.Contains(low, `"code":11128`) || strings.Contains(low, `"code":"11128"`)
}

// isPromptTooLong covers the oversized-input family the upstream answers with
// 413 or business code 11115.
func isPromptTooLong(status int, payload string) bool {
	return status == http.StatusRequestEntityTooLarge || codeIn(payload, 11115)
}

// isModelNotRegistered covers the model-catalog rejection (11102), which is
// model-shaped rather than credential-shaped.
func isModelNotRegistered(payload string) bool {
	return codeIn(payload, 11102)
}

// isModelScopedRateLimit covers the 6004 frequency limit: the upstream itself
// says "switch to another model to continue", so the credential must stay
// healthy and only the (credential, model) pair is parked.
func isModelScopedRateLimit(payload string) bool {
	return codeIn(payload, 6004)
}

// isWafBlocked reports a bare 403 challenge: no business envelope means the
// edge refused the request shape, not the account.
func isWafBlocked(status int, payload string) bool {
	return status == http.StatusForbidden && !hasBusinessEnvelope(payload)
}

// hasBusinessEnvelope reports whether the payload is a JSON business error
// (anything carrying a "code" field) rather than an HTML/text edge response.
func hasBusinessEnvelope(payload string) bool {
	var shape struct {
		Code json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal([]byte(payload), &shape); err != nil {
		return false
	}
	return len(shape.Code) > 0
}

// codeIn reports whether the payload carries the given business error code, in
// either the numeric or the quoted-string spelling.
func codeIn(payload string, code int) bool {
	var shape struct {
		Code json.RawMessage `json:"code"`
	}
	if err := json.Unmarshal([]byte(payload), &shape); err == nil && len(shape.Code) > 0 {
		var nums []int
		var num int
		if json.Unmarshal(shape.Code, &num) == nil {
			nums = append(nums, num)
		}
		var str string
		if json.Unmarshal(shape.Code, &str) == nil {
			if _, err := fmt.Sscanf(strings.TrimSpace(str), "%d", &num); err == nil {
				nums = append(nums, num)
			}
		}
		for _, n := range nums {
			if n == code {
				return true
			}
		}
	}
	low := strings.ToLower(payload)
	return strings.Contains(low, fmt.Sprintf(`"code":%d`, code)) ||
		strings.Contains(low, fmt.Sprintf(`"code":"%d"`, code))
}
