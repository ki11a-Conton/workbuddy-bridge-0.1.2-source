// offline_test.go — proxy-off behavior. The user requirement: CPA startup must
// stay clean and the models must stay registered even when the machine-local
// proxy (Clash Verge 7897) is NOT running; requests only work when the proxy
// is up. This test proves the fallback path in isolation by pointing the
// proxy at an unused port (connection refused in milliseconds, exactly like
// the real 7897 port when Clash Verge is down) — without touching the user's
// live proxy or network.
package main

import (
	"context"
	"testing"
	"time"
)

func TestCatalogForFallbackWhenProxyOff(t *testing.T) {
	prev := localProxy
	localProxy = "http://127.0.0.1:59999" // nothing listens here -> refused
	defer func() { localProxy = prev }()

	c := &credential{UID: "test-uid", AccessToken: "unused", Domain: "workbuddy.ai"}
	models, err := catalogFor(context.Background(), c)
	if err != nil {
		t.Fatalf("catalogFor with proxy off returned error: %v (must degrade, not fail)", err)
	}
	if len(models) != len(staticFallback) {
		t.Fatalf("fallback model count = %d, want %d", len(models), len(staticFallback))
	}
	if models[0].ID != staticFallback[0].ID {
		t.Fatalf("fallback model id = %q, want %q", models[0].ID, staticFallback[0].ID)
	}
	if models[0].ID != "deepseek-v4.1-flash" {
		t.Fatalf("fallback model id = %q, want deepseek-v4.1-flash", models[0].ID)
	}
}

// The cached-entry path must still return the live roster when it exists
// (regression guard: fallback must never overwrite a good cache).
func TestCatalogForUsesCache(t *testing.T) {
	prev := localProxy
	localProxy = "http://127.0.0.1:59999"
	defer func() { localProxy = prev }()

	c := &credential{UID: "test-cached", AccessToken: "unused", Domain: "workbuddy.ai"}
	want := []modelInfo{{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash"}}
	catalogCache.Store(c.UID, catalogEntry{at: time.Now(), models: want})
	models, err := catalogFor(context.Background(), c)
	if err != nil {
		t.Fatalf("catalogFor with warm cache returned error: %v", err)
	}
	if len(models) != 1 || models[0].ID != "deepseek-v4.1-flash" {
		t.Fatalf("cached models = %+v, want cached deepseek-v4.1-flash", models)
	}
	catalogCache.Delete(c.UID)
}