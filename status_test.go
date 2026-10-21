package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func decodeEnvelope(t *testing.T, raw []byte) rpcEnvelope {
	t.Helper()
	var env rpcEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("envelope is not JSON: %v (%s)", err, raw)
	}
	if env.Error == nil {
		t.Fatalf("envelope carries no error: %s", raw)
	}
	return env
}

// TestUpstreamStatusError11128CarriesRequestScopedStatus pins the wire shape the
// host decodes. Measured on this bridge 2026-10-21: an intl body carrying a
// blocked phrase answered 400/11128, and because the executor returned a bare
// error the host read it as a credential failure — it cooled the account and
// the very next call died with "auth_unavailable" (503). The 11128 family is
// request-shaped, so it must cross the RPC envelope as http_status=400, which
// makes the host classify it request-scoped and stop after the first rejection.
func TestUpstreamStatusError11128CarriesRequestScopedStatus(t *testing.T) {
	payload := `{"code":11128,"msg":"Illegal API invocation from an unapproved channel","requestId":"a02219ca-9976-4475-9213-7f3dc5b21d5b"}`
	inner := errors.New("upstream http 400: " + payload)

	wrapped := upstreamStatusError(http.StatusBadRequest, payload, inner)

	var sc interface{ StatusCode() int }
	if !errors.As(wrapped, &sc) {
		t.Fatalf("11128 must wrap a status carrier, got %T", wrapped)
	}
	if got := sc.StatusCode(); got != http.StatusBadRequest {
		t.Fatalf("StatusCode() = %d, want 400 (request-scoped)", got)
	}

	env := decodeEnvelope(t, errorEnvelopeFor(wrapped))
	if env.Error.HTTPStatus != http.StatusBadRequest {
		t.Fatalf("envelope http_status = %d, want 400 (v8 IsRequestFault keying)", env.Error.HTTPStatus)
	}
	if env.Error.Code != "plugin_error" {
		t.Fatalf("envelope code = %q, want plugin_error (mirrors the CN plugin)", env.Error.Code)
	}
	if !strings.Contains(env.Error.Message, "11128") {
		t.Fatalf("envelope message lost the upstream code: %q", env.Error.Message)
	}
}

// TestUpstreamStatusErrorAccountVsRequest pins the cooldown policy: only
// account-level shapes may reach the host with a status, because a status is
// what makes the host cool the credential sit out its window.
func TestUpstreamStatusErrorAccountVsRequest(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		payload string
		want    int
	}{
		{"11128 on 400 stays request-scoped", 400, `{"code":11128,"msg":"channel risk"}`, 400},
		{"11128 on 403 also request-scoped", 403, `{"code":11128,"msg":"channel risk"}`, 400},
		{"11128 quoted-string variant", 400, `{"code":"11128","msg":"blocked"}`, 400},
		{"401 dead token", 401, `{"code":1001,"msg":"invalid token"}`, 401},
		{"402 credits", 402, `{"code":1002,"msg":"insufficient credits"}`, 402},
		{"429 quota", 429, `{"code":1003,"msg":"rate limited"}`, 429},
		{"business 403", 403, `{"code":11140,"msg":"permission denied"}`, 403},
		{"bare 403 WAF challenge", 403, `<html>access denied</html>`, 0},
		{"413 prompt overflow", 413, `{"msg":"too large"}`, 0},
		{"11115 prompt overflow", 400, `{"code":11115,"msg":"prompt too long"}`, 0},
		{"11102 model not registered", 400, `{"code":11102,"msg":"model not registered"}`, 0},
		{"6004 model-scoped limit keeps 429 as plain", 429, `{"code":6004,"msg":"switch to another model to continue"}`, 0},
		{"11133 effort rejection", 400, `{"code":11133,"msg":"reasoning.effort not supported"}`, 0},
		{"plain 500", 500, `oops`, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := errors.New("upstream http " + strconv.Itoa(tc.status))
			got := 0
			var sc interface{ StatusCode() int }
			if wrapped := upstreamStatusError(tc.status, tc.payload, inner); errors.As(wrapped, &sc) {
				got = sc.StatusCode()
			}
			if got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestErrorEnvelopeForPlainErrorOmitsStatus keeps the pre-existing shape for
// errors that carry no status: the host then applies only its default
// transient cooldown instead of a per-status one.
func TestErrorEnvelopeForPlainErrorOmitsStatus(t *testing.T) {
	raw := errorEnvelopeFor(errors.New("upstream transport: dial tcp: i/o timeout"))
	if strings.Contains(string(raw), "http_status") {
		t.Fatalf("plain error must not invent a status: %s", raw)
	}
	env := decodeEnvelope(t, raw)
	if env.Error.Code != "internal" {
		t.Fatalf("code = %q, want internal", env.Error.Code)
	}
}
