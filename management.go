// management.go — the workbuddy-bridge management surface: a sidebar panel
// ("WorkBuddy Bridge") served at /v0/resource/plugins/workbuddy-bridge/panel and
// one JSON route /v0/management/plugins/workbuddy-bridge/accounts listing every
// credential with its live credit balance. Registered the way the
// cpa-multi-plugins plugins do it (Resources → sidebar menu, Routes → proxied
// management API, both dispatched through management.handle).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// Registration shapes mirror the other plugins' local declarations: Routes are
// host-proxied management APIs, Resources are browser-facing pages under
// /v0/resource/plugins/<provider>.
type managementRoute struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Description string `json:"description,omitempty"`
}

type resourceRoute struct {
	Path        string `json:"path"`
	Menu        string `json:"menu,omitempty"`
	Description string `json:"description,omitempty"`
}

type managementRegistrationResponse struct {
	Routes    []managementRoute `json:"routes,omitempty"`
	Resources []resourceRoute   `json:"resources,omitempty"`
}

// absolute request paths the host forwards to management.handle.
const (
	mgmtAccountsPath = "/v0/management/plugins/" + providerName + "/accounts"
	mgmtPanelPath    = "/v0/resource/plugins/" + providerName + "/panel"
)

func managementRegistration() managementRegistrationResponse {
	return managementRegistrationResponse{
		Routes: []managementRoute{
			{Method: http.MethodGet, Path: "/accounts", Description: "账号与积分（JSON）"},
		},
		Resources: []resourceRoute{
			{Path: "/panel", Menu: "WorkBuddy Bridge", Description: "国际版 WorkBuddy AI 账号与积分面板（dsh-workbuddy-bridge 移植）"},
		},
	}
}

func handleManagement(raw []byte) ([]byte, error) {
	var req pluginapi.ManagementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, err
	}
	path := strings.TrimRight(req.Path, "/")
	if req.Method == http.MethodGet && path == mgmtAccountsPath {
		return okEnvelope(mgmtJSONResponse(http.StatusOK, accountsPayload())), nil
	}
	if req.Method == http.MethodGet && path == mgmtPanelPath {
		return okEnvelope(mgmtHTMLResponse([]byte(panelHTML))), nil
	}
	return okEnvelope(mgmtJSONResponse(http.StatusNotFound, map[string]any{"error": "not found: " + path})), nil
}

func mgmtJSONResponse(status int, v any) pluginapi.ManagementResponse {
	body, _ := json.Marshal(v)
	h := http.Header{}
	h.Set("Content-Type", "application/json; charset=utf-8")
	return pluginapi.ManagementResponse{StatusCode: status, Headers: h, Body: body}
}

func mgmtHTMLResponse(body []byte) pluginapi.ManagementResponse {
	h := http.Header{}
	h.Set("Content-Type", "text/html; charset=utf-8")
	return pluginapi.ManagementResponse{StatusCode: http.StatusOK, Headers: h, Body: body}
}

// accountRow is one panel row: identity + live credit balance.
type accountRow struct {
	Name     string `json:"name"`
	UID      string `json:"uid"`
	Nickname string `json:"nickname"`
	Domain   string `json:"domain"`
	Credits  string `json:"credits"`
	Disabled bool   `json:"disabled"`
	Error    string `json:"error,omitempty"`
}

// accountsPayload lists every credential owned by this provider and fetches
// each one's credit balance concurrently (bounded at 6 in flight, 20s budget).
func accountsPayload() []any {
	entries, err := hostAuthList()
	if err != nil {
		return []any{map[string]any{"error": "auth list: " + err.Error()}}
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		rows = make([]any, 0, len(entries))
		sem  = make(chan struct{}, 6)
	)
	for _, e := range entries {
		entry := e
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			row := fetchAccountRow(entry)
			mu.Lock()
			rows = append(rows, row)
			mu.Unlock()
		}()
	}
	wg.Wait()
	return rows
}

func fetchAccountRow(entry pluginapi.HostAuthFileEntry) accountRow {
	row := accountRow{
		Name:     entry.Name,
		UID:      entry.ID,
		Disabled: entry.Disabled,
	}
	raw, err := hostAuthGetJSON(entry.AuthIndex)
	if err != nil {
		row.Error = "credential read: " + err.Error()
		return row
	}
	c, err := parseCredential(raw)
	if err != nil {
		row.Error = "credential parse: " + err.Error()
		return row
	}
	row.UID = shortUID(c.UID, row.UID)
	row.Nickname = c.Nickname
	row.Domain = c.Domain
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	body, err := fetchCreditsBytes(ctx, c)
	if err != nil {
		row.Error = err.Error()
		return row
	}
	env, err := parseEnvelope(body)
	if err != nil {
		row.Error = err.Error()
		return row
	}
	cs, err := parseCredits(env.Data)
	if err != nil {
		row.Error = err.Error()
		return row
	}
	if cs.Balance != "" {
		row.Credits = cs.Balance
	} else if cs.BalanceNumeric != 0 {
		row.Credits = fmt.Sprintf("%g", cs.BalanceNumeric)
	}
	return row
}

// shortUID keeps the row readable when the host id is an empty placeholder.
func shortUID(uid string, fallback string) string {
	if uid == "" {
		return fallback
	}
	if len(uid) > 12 {
		return uid[:6] + "…" + uid[len(uid)-6:]
	}
	return uid
}

// hostEnvelope mirrors the host RPC reply envelope (ok/result/error).
type hostEnvelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result"`
	Error  *envelopeError  `json:"error"`
}

func hostAuthList() ([]pluginapi.HostAuthFileEntry, error) {
	raw, err := hostCall(pluginabi.MethodHostAuthList, nil)
	if err != nil {
		return nil, err
	}
	var env hostEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if !env.OK || env.Error != nil {
		msg := ""
		if env.Error != nil {
			msg = env.Error.Message
		}
		return nil, fmt.Errorf("host.auth.list: ok=%v %s", env.OK, msg)
	}
	var resp struct {
		Files []pluginapi.HostAuthFileEntry `json:"files"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		return nil, err
	}
	out := make([]pluginapi.HostAuthFileEntry, 0, len(resp.Files))
	for _, f := range resp.Files {
		if strings.HasPrefix(strings.ToLower(f.Name), providerName+"-") {
			out = append(out, f)
		}
	}
	return out, nil
}

func hostAuthGetJSON(authIndex string) (json.RawMessage, error) {
	body, _ := json.Marshal(map[string]any{"auth_index": authIndex})
	raw, err := hostCall(pluginabi.MethodHostAuthGet, body)
	if err != nil {
		return nil, err
	}
	var env hostEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if !env.OK || env.Error != nil {
		return nil, fmt.Errorf("host.auth.get: auth_index=%s", authIndex)
	}
	var resp struct {
		JSON json.RawMessage `json:"json"`
	}
	if err := json.Unmarshal(env.Result, &resp); err != nil {
		return nil, err
	}
	return resp.JSON, nil
}