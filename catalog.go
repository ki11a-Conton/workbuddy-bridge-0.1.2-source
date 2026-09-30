// catalog.go — international model catalog: GET /v3/config with the
// app-shaped User-Agent, intersect the product rows with the `cli` agent
// roster, and merge promotions. Parsing mirrors the bridge's parseModelCatalog
// behavior, unit-tested against a real captured document
// (testdata/intl-config-real.json).
package main

import (
	"encoding/json"
	"fmt"
)

// modelInfo is the plugin-facing description of one usable international model.
type modelInfo struct {
	ID                 string
	Name               string
	Credits            string
	ContextWindow      int64
	MaxInputTokens     int64
	MaxOutputTokens    int64
	SupportsImages     bool
	SupportsToolCall   bool
	SupportsReasoning  bool
	OnlyReasoning      bool
	HasThinking        bool
	CanDisableThinking bool
	DefaultEffort      string
	SupportedEfforts   []string
	Tags               []string
	IsDefault          bool
}

// promotion is one entry of the product document's discount list.
type promotion struct {
	ID                string
	Label             string
	DiscountedCredits string
	Factor            float64
	Enabled           bool
	ModelIDs          []string
	ValidFrom         string
	ValidUntil        string
}

// parseCatalog returns the usable models (cli roster ∩ product rows) and the
// promotion list. Rows without an id, disabled rows, and rows with non-positive
// caps drop out; image/video rows are excluded implicitly because they are not
// on the cli roster.
func parseCatalog(raw []byte) ([]modelInfo, []promotion, error) {
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, nil, fmt.Errorf("catalog is not JSON: %w", err)
	}
	data := envelopeData(doc)
	roster := cliRoster(data)
	promos := parsePromotions(data["modelPromotions"])

	rows, ok := data["models"].([]any)
	if !ok {
		return nil, promos, nil
	}
	var out []modelInfo
	for _, r := range rows {
		row, ok := r.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		if id == "" || !roster[id] {
			continue
		}
		if disabled, _ := row["disabled"].(bool); disabled {
			continue
		}
		mi := modelInfo{
			ID:                id,
			Name:              str(row["name"]),
			Credits:           str(row["credits"]),
			MaxInputTokens:    num(row["maxInputTokens"]),
			MaxOutputTokens:   num(row["maxOutputTokens"]),
			SupportsImages:    boolv(row["supportsImages"]),
			SupportsToolCall:  boolv(row["supportsToolCall"]),
			SupportsReasoning: boolv(row["supportsReasoning"]),
			OnlyReasoning:     boolv(row["onlyReasoning"]),
			IsDefault:         boolv(row["isDefault"]),
			Tags:              strSlice(row["tags"]),
		}
		if mi.MaxInputTokens <= 0 || mi.MaxOutputTokens <= 0 {
			continue
		}
		if cw, ok := row["contextWindow"].(map[string]any); ok {
			mi.ContextWindow = num(cw["defaultLength"])
		}
		if mi.ContextWindow <= 0 {
			mi.ContextWindow = num(row["maxAllowedSize"])
		}
		if rea, ok := row["reasoning"].(map[string]any); ok {
			mi.HasThinking = true
			mi.CanDisableThinking = boolv(rea["canDisableThinking"])
			mi.DefaultEffort = str(rea["defaultEffort"])
			mi.SupportedEfforts = strSlice(rea["supportedEfforts"])
		}
		out = append(out, mi)
	}
	return out, promos, nil
}

// envelopeData unwraps {code,msg,data} and tolerates the bare product document
// shape (a body carrying models or agents with no wrapper).
func envelopeData(doc map[string]any) map[string]any {
	if data, ok := doc["data"].(map[string]any); ok {
		return data
	}
	if _, hasModels := doc["models"]; hasModels {
		return doc
	}
	if _, hasAgents := doc["agents"]; hasAgents {
		return doc
	}
	return map[string]any{}
}

// cliRoster returns the set of model ids the `cli` agent may chat with; the
// gateway answers /v3/config differently per client identity, and the roster
// is the authoritative usable subset.
func cliRoster(data map[string]any) map[string]bool {
	out := map[string]bool{}
	agents, ok := data["agents"].([]any)
	if !ok {
		return out
	}
	for _, a := range agents {
		agent, ok := a.(map[string]any)
		if !ok || agent["name"] != "cli" {
			continue
		}
		for _, id := range strSlice(agent["models"]) {
			out[id] = true
		}
	}
	return out
}

func parsePromotions(raw any) []promotion {
	var out []promotion
	list, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, p := range list {
		item, ok := p.(map[string]any)
		if !ok {
			continue
		}
		pr := promotion{
			ID:      str(item["id"]),
			Enabled: boolv(item["enabled"]),
			ModelIDs: strSlice(item["modelIds"]),
		}
		if badge, ok := item["badge"].(map[string]any); ok {
			pr.Label = str(badge["label"])
		}
		if disc, ok := item["discount"].(map[string]any); ok {
			pr.DiscountedCredits = str(disc["discountedCredits"])
			pr.Factor = floatnum(disc["factor"])
		}
		if sched, ok := item["schedule"].(map[string]any); ok {
			pr.ValidFrom = str(sched["validFrom"])
			pr.ValidUntil = str(sched["validUntil"])
		}
		out = append(out, pr)
	}
	return out
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func boolv(v any) bool {
	b, _ := v.(bool)
	return b
}

func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case int64:
		return n
	}
	return 0
}

func floatnum(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case json.Number:
		f, _ := n.Float64()
		return f
	}
	return 0
}

func strSlice(v any) []string {
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
