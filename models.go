// models.go — ModelProvider handlers: the plugin has no static models (the
// roster is per-account), and ModelsForAuth fetches the international /v3/config
// document with that account's identity (app-shaped UA), caches it five
// minutes, and maps the cli-roster rows into pluginapi.ModelInfo.
//
// Proxy independence: the catalog probe goes through the machine-local proxy
// (Clash Verge 7897). When the proxy is NOT running (or the upstream is
// unreachable), the fetch must NOT hard-fail: a hard error makes the host skip
// model registration entirely (so the models disappear and the log fills with
// "models for auth ... failed" per auth) and CPA startup looks broken. Instead
// a failed probe degrades to the static allow-list, so the models stay
// registered in CPA at all times — they answer requests when the proxy is up
// and fail with a clean transport error when it is down (exactly "proxy on =>
// usable, proxy off => unusable, startup unaffected").
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var catalogCache sync.Map // key uid -> catalogEntry

type catalogEntry struct {
	at     time.Time
	models []modelInfo
}

const catalogTTL = 5 * time.Minute

// staticFallback is the offline allow-list used when the live catalog is
// unreachable (proxy down / upstream blocked). Keep it in sync with the
// cli-roster ids of the international /v3/config document.
var staticFallback = []modelInfo{
	{ID: "deepseek-v4.1-flash", Name: "DeepSeek V4.1 Flash",
		MaxInputTokens: 262144, MaxOutputTokens: 8192},
}

// lastCatalogLogAt throttles the repeated "catalog fetch failed" log so 31
// auths do not spam startup output.
var (
	lastCatalogLogMu sync.Mutex
	lastCatalogLogAt time.Time
)

func catalogFor(ctx context.Context, c *credential) ([]modelInfo, error) {
	if v, ok := catalogCache.Load(c.UID); ok {
		e := v.(catalogEntry)
		if time.Since(e.at) < catalogTTL {
			return e.models, nil
		}
	}
	raw, err := fetchCatalogBytes(ctx, c)
	if err == nil {
		models, _, parseErr := parseCatalog(raw)
		if parseErr != nil {
			log.Printf("wbai catalog: uid=%s parse err=%v (using fallback)", c.UID, parseErr)
		} else {
			log.Printf("wbai catalog: uid=%s body=%dB models=%d", c.UID, len(raw), len(models))
			catalogCache.Store(c.UID, catalogEntry{at: time.Now(), models: models})
			return models, nil
		}
	} else {
		// Proxy down / upstream unreachable: degrade to the static list instead
		// of erroring, and keep the log quiet (rate-limited).
		lastCatalogLogMu.Lock()
		quiet := time.Since(lastCatalogLogAt) < 30*time.Second
		if !quiet {
			lastCatalogLogAt = time.Now()
		}
		lastCatalogLogMu.Unlock()
		if !quiet {
			log.Printf("wbai catalog: uid=%s fetch err=%v (proxy offline? using static fallback)", c.UID, err)
		}
	}
	return staticFallback, nil
}

func handleModelStatic(raw []byte) ([]byte, error) {
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName}), nil
}

func handleModelForAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse model request: %w", err)
	}
	c, err := parseCredential(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	infos, err := catalogFor(context.Background(), c)
	if err != nil {
		return nil, err
	}
	// Host-side exclusion list (config `oauth.excluded-models.<provider>`):
	// the user's pattern for exposing exactly one free model. Anything listed
	// never reaches the picker, no matter what the upstream catalog contains.
	excluded := map[string]bool{}
	for _, name := range req.Host.ExcludedModels[providerName] {
		excluded[strings.ToLower(strings.TrimSpace(name))] = true
	}
	if len(excluded) > 0 {
		kept := infos[:0]
		for _, mi := range infos {
			if !excluded[strings.ToLower(mi.ID)] {
				kept = append(kept, mi)
			}
		}
		infos = kept
	}
	models := make([]pluginapi.ModelInfo, 0, len(infos))
	for _, mi := range infos {
		modalities := []string{"text"}
		if mi.SupportsImages {
			modalities = append(modalities, "image")
		}
		var thinking *pluginapi.ThinkingSupport
		if mi.HasThinking {
			thinking = &pluginapi.ThinkingSupport{
				Levels:      mi.SupportedEfforts,
				ZeroAllowed: mi.CanDisableThinking,
			}
		}
		models = append(models, pluginapi.ModelInfo{
			ID:                       mi.ID,
			Object:                   "model",
			Name:                     mi.ID,
			DisplayName:              mi.Name,
			OwnedBy:                  "workbuddy",
			InputTokenLimit:          mi.MaxInputTokens,
			OutputTokenLimit:         mi.MaxOutputTokens,
			ContextLength:            mi.ContextWindow,
			MaxCompletionTokens:      mi.MaxOutputTokens,
			SupportedInputModalities: modalities,
			Thinking:                 thinking,
		})
	}
	return okEnvelope(pluginapi.ModelResponse{Provider: providerName, Models: models}), nil
}