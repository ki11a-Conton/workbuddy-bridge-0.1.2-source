// billing.go — international billing/credits, ported from the bridge's
// billing path. The exact /v2/billing/meter/get-user-resource response shape is
// verified against the live endpoint on first plugin run; the envelope reader
// here is exercised by the catalog fixture today and the credits parser is
// deliberately conservative (missing/unknown fields degrade to zero values).
package main

import (
	"encoding/json"
	"fmt"
)

// envelope is the {code,msg,data} wrapper most upstream JSON endpoints answer
// with (catalog, refresh, billing).
type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

func parseEnvelope(raw []byte) (envelope, error) {
	var e envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return e, fmt.Errorf("envelope is not JSON: %w", err)
	}
	if e.Code != 0 {
		return e, fmt.Errorf("upstream envelope error code=%d msg=%q", e.Code, e.Msg)
	}
	return e, nil
}

// creditsSummary is the agreed reading of the billing payload. Verified live
// once the plugin is deployed; until then, anything unrecognized reads as zero.
type creditsSummary struct {
	// Balance is the human string the endpoint reports for total remaining
	// credits (e.g. "x123.45" or plain number text).
	Balance string
	// BalanceNumeric is a parsed numeric balance when the payload carries one.
	BalanceNumeric float64
}

// parseCredits reads the billing data payload for a credits summary.
func parseCredits(raw []byte) (creditsSummary, error) {
	var out creditsSummary
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return out, fmt.Errorf("billing payload is not JSON: %w", err)
	}
	for _, key := range []string{"credits", "totalCredits", "balance", "total"} {
		if v, ok := obj[key]; ok {
			switch n := v.(type) {
			case string:
				out.Balance = n
			case float64:
				out.BalanceNumeric = n
			case json.Number:
				f, _ := n.Float64()
				out.BalanceNumeric = f
			}
			if out.Balance != "" || out.BalanceNumeric != 0 {
				break
			}
		}
	}
	return out, nil
}