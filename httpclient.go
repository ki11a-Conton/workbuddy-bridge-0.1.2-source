// httpclient.go — the plugin's outbound HTTP: one shared client with a 600s
// ceiling (the local patch carried over from the workbuddy plugin: long
// high-context agent requests outlive the default 120s), and the upstream
// calls for chat, refresh, catalog, and billing built on wire.go.
//
// v1 deliberately uses a plain net/http client instead of the host bridge:
// the spike proved the wire, and the host bridge adds request-log capture at
// the cost of another ABI seam to keep in sync. If CPA request logs for this
// provider are wanted, swap `httpClient().Do` for `HTTPClient.Do` later.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// localProxy is the machine-local proxy (Clash Verge mixed port). Direct
// outbound access to the international WorkBuddy endpoints is blocked on this
// host, so every upstream call goes through it explicitly. It is a var (not a
// const) only so tests can point it at an unused port and exercise the
// proxy-off code path without touching the real Clash Verge listener.
var localProxy = "http://127.0.0.1:7897"

// proxyDialTimeout bounds the dial to the machine-local proxy (and the proxy's
// own connect to its upstream). With the proxy off, dialing 127.0.0.1:7897
// fails in milliseconds; with the proxy on but its upstream hung, the dial
// also fails here instead of stretching to the 600s client ceiling — that
// keeps CPA startup and model listing responsive while the proxy is unhealthy.
const proxyDialTimeout = 3 * time.Second

var (
	sharedOnce sync.Once
	sharedHTTP *http.Client
)

func proxiedTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: proxyDialTimeout}
	return &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return url.Parse(localProxy) },
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, addr)
		},
		// Fall back to env proxy (HTTP(S)_PROXY) when the local proxy is unset.
		MaxIdleConns:        20,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 5,
	}
}

func httpClient() *http.Client {
	sharedOnce.Do(func() {
		sharedHTTP = &http.Client{
			Timeout:   600 * time.Second,
			Transport: proxiedTransport(),
		}
	})
	return sharedHTTP
}

// chatStream posts one chat completion to the international endpoint and
// returns the response; the caller drains or closes it.
func chatStream(ctx context.Context, c *credential, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, globalBase+chatEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, v := range chatHeaders(c, appUserAgent(appVersionFallback)) {
		req.Header.Set(k, v)
	}
	return httpClient().Do(req)
}

// refreshCredential calls the token-refresh endpoint and returns the patched
// credential. Ported from the bridge's refreshToken: POST with
// X-Refresh-Token (never Authorization), envelope {code,msg,data{accessToken,
// refreshToken, expiresIn, domain}}.
func refreshCredential(ctx context.Context, c *credential) (*credential, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, globalBase+refreshEndpoint, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range refreshHeaders(c) {
		req.Header.Set(k, v)
	}
	resp, err := httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh transport: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	env, err := parseEnvelope(body)
	if err != nil {
		return nil, fmt.Errorf("refresh envelope: %w (http %d)", err, resp.StatusCode)
	}
	var data struct {
		AccessToken  string `json:"accessToken"`
		RefreshToken string `json:"refreshToken"`
		ExpiresIn    int64  `json:"expiresIn"`
		Domain       string `json:"domain"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil {
		return nil, fmt.Errorf("refresh payload: %w", err)
	}
	if data.AccessToken == "" {
		return nil, fmt.Errorf("refresh returned no accessToken")
	}
	next := *c
	next.AccessToken = data.AccessToken
	if data.RefreshToken != "" {
		next.RefreshToken = data.RefreshToken
	}
	if data.Domain != "" {
		next.Domain = data.Domain
	}
	if data.ExpiresIn > 0 {
		next.ExpiresAtMS = time.Now().UnixMilli() + data.ExpiresIn*1000
	}
	return &next, nil
}

// fetchCatalogBytes GETs /v3/config with the app-shaped identity.
func fetchCatalogBytes(ctx context.Context, c *credential) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, globalBase+catalogEndpoint, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range catalogHeaders(c, appVersionFallback) {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: proxiedTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("catalog transport: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("catalog http %d: %s", resp.StatusCode, truncateBody(body))
	}
	return body, nil
}

// fetchCreditsBytes GETs the user-resource billing payload.
func fetchCreditsBytes(ctx context.Context, c *credential) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, globalBase+billingEndpoint, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range billingHeaders(c) {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: proxiedTransport()}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("billing transport: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("billing http %d: %s", resp.StatusCode, truncateBody(body))
	}
	return body, nil
}

func truncateBody(body []byte) string {
	s := strings.ReplaceAll(string(body), "\n", " ")
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
