// auth.go — AuthProvider handlers: ParseAuth (claim global workbuddy-ai
// credential files only) and RefreshAuth (port of the bridge refresh). Login
// flows are deliberately unsupported in v1: credentials are adopted as files
// (workbuddy-ai-*.json copies of the global accounts), so CPA never needs a
// browser login for this provider.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// isOursAuthFile reports whether a credential file belongs to this provider:
// an explicit type of workbuddy-ai/workbuddy-global/workbuddy (when the domain
// is global), or a type-less file whose name carries the workbuddy-ai- prefix.
func isOursAuthFile(fileName, declaredType string) bool {
	t := strings.ToLower(strings.TrimSpace(declaredType))
	switch t {
	case "workbuddy-ai", "workbuddy-global", "workbuddy", providerName:
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(fileName))
	return strings.HasPrefix(lower, providerName+"-")
}

func handleParseAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse auth request: %w", err)
	}
	var probe struct {
		Type   string `json:"type"`
		Domain string `json:"domain"`
	}
	_ = json.Unmarshal(req.RawJSON, &probe)
	// The host routes typed files by type; type-less files are polled to every
	// plugin and the first Handled=true wins. Claim only files that are ours
	// AND parse as an international credential — anything else stays unclaimed
	// so the CN/global workbuddy plugin keeps its own files.
	if probe.Type != "" && !isOursAuthFile("", probe.Type) {
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false}), nil
	}
	if probe.Type == "" && !isOursAuthFile(req.FileName, "") {
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false}), nil
	}
	c, err := parseCredential(req.RawJSON)
	if err != nil {
		// CN-domain or malformed: not ours.
		return okEnvelope(pluginapi.AuthParseResponse{Handled: false}), nil
	}
	_ = c
	auth := pluginapi.AuthData{
		Provider:    providerName,
		FileName:    req.FileName,
		Label:       labelOf(req.RawJSON),
		StorageJSON: req.RawJSON,
		Attributes: map[string]string{
			"uid":    c.UID,
			"domain": c.Domain,
			"region": "global",
		},
	}
	return okEnvelope(pluginapi.AuthParseResponse{Handled: true, Auth: auth}), nil
}

// labelOf reads the human nickname from a credential file without failing the
// parse when it is absent.
func labelOf(raw []byte) string {
	var probe struct {
		Account struct {
			Nickname string `json:"nickname"`
		} `json:"account"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Account.Nickname != "" {
		return probe.Account.Nickname
	}
	return "workbuddy-ai account"
}

func handleRefreshAuth(raw []byte) ([]byte, error) {
	var req pluginapi.AuthRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse refresh request: %w", err)
	}
	c, err := parseCredential(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	next, err := refreshCredential(context.Background(), c)
	if err != nil {
		log.Printf("wbai refresh: uid=%s err=%v", c.UID, err)
		return nil, err
	}
	log.Printf("wbai refresh: uid=%s ok", c.UID)
	// Patch the persisted credential JSON with the fresh tokens.
	var doc cpaAuthFile
	if err := json.Unmarshal(req.StorageJSON, &doc); err != nil {
		return nil, fmt.Errorf("storage not a cpa auth file: %w", err)
	}
	doc.Auth.AccessToken = next.AccessToken
	doc.Auth.RefreshToken = next.RefreshToken
	doc.Auth.ExpiresAt = next.ExpiresAtMS
	doc.Auth.Domain = next.Domain
	fresh, _ := json.Marshal(doc)
	auth := pluginapi.AuthData{
		Provider:     providerName,
		ID:           req.AuthID,
		FileName:     "",
		Label:        labelOf(req.StorageJSON),
		StorageJSON:  fresh,
		Attributes: map[string]string{
			"uid":    next.UID,
			"domain": next.Domain,
			"region": "global",
		},
	}
	return okEnvelope(pluginapi.AuthRefreshResponse{
		Auth:             auth,
		NextRefreshAfter: time.Now().Add(24 * time.Hour),
	}), nil
}