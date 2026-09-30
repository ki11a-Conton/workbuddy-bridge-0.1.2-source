// models.go — ModelProvider handlers: the plugin has no static models (the
// roster is per-account), and ModelsForAuth fetches the international /v3/config
// document with that account's identity (app-shaped UA), caches it five
// minutes, and maps the cli-roster rows into pluginapi.ModelInfo.
package main

import (
	"context"
	"encoding/json"
	"fmt"
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

func catalogFor(ctx context.Context, c *credential) ([]modelInfo, error) {
	if v, ok := catalogCache.Load(c.UID); ok {
		e := v.(catalogEntry)
		if time.Since(e.at) < catalogTTL {
			return e.models, nil
		}
	}
	raw, err := fetchCatalogBytes(ctx, c)
	if err != nil {
		return nil, err
	}
	models, _, err := parseCatalog(raw)
	if err != nil {
		return nil, err
	}
	catalogCache.Store(c.UID, catalogEntry{at: time.Now(), models: models})
	return models, nil
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