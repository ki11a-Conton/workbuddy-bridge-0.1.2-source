package main

import (
	"os"
	"testing"
)

// testdata/intl-config.json is a live capture of GET /v3/config with the
// app-shaped User-Agent (26 product rows, cli roster of 23 ids, 3 promotions).
func TestParseCatalog_RealIntlConfig(t *testing.T) {
	raw, err := os.ReadFile("testdata/intl-config.json")
	if err != nil {
		t.Skip("fixture missing: testdata/intl-config.json")
	}
	models, promos, err := parseCatalog(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 23 {
		t.Fatalf("roster intersection: want 23 models, got %d", len(models))
	}

	byID := map[string]modelInfo{}
	for _, m := range models {
		byID[m.ID] = m
	}
	if m, ok := byID["default-model"]; !ok {
		t.Fatal("default-model must be on the roster")
	} else {
		if m.MaxInputTokens != 176000 || m.MaxOutputTokens != 24000 {
			t.Fatalf("default-model caps wrong: %+v", m)
		}
		if !m.SupportsImages || !m.IsDefault {
			t.Fatalf("default-model flags wrong: %+v", m)
		}
	}
	if m, ok := byID["deepseek-v4.1-flash"]; !ok {
		t.Fatal("deepseek-v4.1-flash must be on the roster")
	} else if m.Credits != "x0.00" {
		t.Fatalf("deepseek-v4.1-flash credits wrong: %q", m.Credits)
	}
	if m, ok := byID["fast-model"]; !ok {
		t.Fatal("fast-model must be on the roster")
	} else if !m.OnlyReasoning || m.MaxInputTokens != 200000 {
		t.Fatalf("fast-model wrong: %+v", m)
	}

	// Image/video rows are present in the product document but NOT on the cli
	// roster — they must never reach the picker.
	if _, ok := byID["gpt-image-2.5-sunburst"]; ok {
		t.Fatal("image-generation row leaked onto the cli roster")
	}
	if _, ok := byID["seedance-2.5"]; ok {
		t.Fatal("video-generation row leaked onto the cli roster")
	}

	if len(promos) != 3 {
		t.Fatalf("promotions: want 3, got %d", len(promos))
	}
	found := false
	for _, p := range promos {
		if p.ID == "deepseek-v4.1-flash" {
			found = true
			if p.DiscountedCredits != "0x" || !p.Enabled {
				t.Fatalf("deepseek promo wrong: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("deepseek-v4.1-flash promotion missing")
	}
}

func TestParseCatalog_SyntheticBareDocument(t *testing.T) {
	// The /v3/config endpoint has been observed answering with the product
	// document bare (no {code,msg,data} wrapper); envelopeData must tolerate it.
	raw := []byte(`{"agents":[{"name":"cli","models":["a","b"]}],"models":[{"id":"a","name":"A","maxInputTokens":1000,"maxOutputTokens":1000},{"id":"b","name":"B","maxInputTokens":2000,"maxOutputTokens":2000},{"id":"c","name":"C","maxInputTokens":3000,"maxOutputTokens":3000}]}`)
	models, _, err := parseCatalog(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(models) != 2 || models[0].ID != "a" || models[1].ID != "b" {
		t.Fatalf("bare-document intersection wrong: %+v", models)
	}
}

func TestParseEnvelope_RealCatalogBody(t *testing.T) {
	raw, err := os.ReadFile("testdata/intl-config.json")
	if err != nil {
		t.Skip("fixture missing")
	}
	e, err := parseEnvelope(raw)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	if e.Code != 0 || len(e.Data) == 0 {
		t.Fatalf("envelope wrong: %+v", e)
	}
}