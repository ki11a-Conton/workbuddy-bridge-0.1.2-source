// executor.go — ProviderExecutor handlers for the international wire.
//
// Execute validates and aggregates one chat completion (the upstream only
// accepts stream:true, so the non-stream path runs the same SSE and folds it).
// ExecuteStream forwards the upstream OpenAI-shaped SSE verbatim: sync when the
// host gave no stream id, async via host.stream.emit/close otherwise.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// payloadOf returns the request body the executor should send upstream.
func payloadOf(req pluginapi.ExecutorRequest) []byte {
	if len(req.Payload) > 0 {
		return req.Payload
	}
	return req.OriginalRequest
}

// upstreamBody prepares the outgoing body and rewrites the model id: the host
// passes a client-facing name — either the provider-prefixed form
// ("workbuddy-bridge/deepseek-v4.1-flash") or an oauth-model-alias id
// ("workbuddy-bridge-deepseek-v4.1-flash") — while the international upstream
// only knows the raw catalog id ("deepseek-v4.1-flash"); any wrapped name is
// rejected upstream with code 11102 ("model [...] service info not found").
// The host-resolved upstream id (req.Model) wins when provided.
func upstreamBody(req pluginapi.ExecutorRequest) []byte {
	body := prepareIntlBody(payloadOf(req))
	obj, ok := parseObjectBody(body)
	if !ok {
		return body
	}
	bodyModel, _ := obj["model"].(string)
	target := normalizeUpstreamModel(req.Model)
	if target == "" {
		target = normalizeUpstreamModel(bodyModel)
	}
	if target != "" && target != bodyModel {
		obj["model"] = target
		return mustJSON(obj)
	}
	return body
}

// normalizeUpstreamModel maps any client-facing spelling of a catalog model id
// back to the raw id the upstream accepts ("workbuddy-bridge/<id>" and
// "workbuddy-bridge-<id>" both become "<id>"; a bare id passes through).
func normalizeUpstreamModel(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, providerName+"/") {
		s = strings.TrimPrefix(s, providerName+"/")
	} else if strings.HasPrefix(s, providerName+"-") {
		s = strings.TrimPrefix(s, providerName+"-")
	}
	return strings.TrimSpace(s)
}

func credentialOf(req pluginapi.ExecutorRequest) (*credential, error) {
	return parseCredential(req.StorageJSON)
}

func handleExecExecute(raw []byte) ([]byte, error) {
	var req pluginapi.ExecutorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse execute request: %w", err)
	}
	c, err := credentialOf(req)
	if err != nil {
		return nil, err
	}
	body := upstreamBody(req)
	resp, err := chatStream(context.Background(), c, body)
	if err != nil {
		return nil, fmt.Errorf("upstream transport: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, errorBodyLimit))
		payload := string(errBody)
		return nil, upstreamStatusError(resp.StatusCode, payload,
			fmt.Errorf("upstream http %d: %s", resp.StatusCode, truncateBody(errBody)))
	}
	completion, err := aggregateCompletion(resp.Body, req.Model)
	if err != nil {
		return nil, err
	}
	return okEnvelope(pluginapi.ExecutorResponse{Payload: completion}), nil
}

func handleExecStream(raw []byte) ([]byte, error) {
	var req executorStreamRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse stream request: %w", err)
	}
	c, err := credentialOf(req.ExecutorRequest)
	if err != nil {
		return nil, err
	}
	body := upstreamBody(req.ExecutorRequest)
	upstream, err := chatStream(context.Background(), c, body)
	if err != nil {
		return nil, fmt.Errorf("upstream transport: %w", err)
	}
	if upstream.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(upstream.Body, errorBodyLimit))
		upstream.Body.Close()
		payload := string(errBody)
		return nil, upstreamStatusError(upstream.StatusCode, payload,
			fmt.Errorf("upstream http %d: %s", upstream.StatusCode, truncateBody(errBody)))
	}

	headers := streamHeaders()
	sseFramed := clientNeedsSSEFrame(req.Metadata)

	// Async: the host created the stream and will read host.stream.emit pushes.
	// Each upstream data event is cleaned and emitted as a bare JSON payload —
	// the host adds the "data: " framing and its own [DONE] terminator.
	// NOTE: the upstream body is closed INSIDE the pump goroutine, never by a
	// defer here — returning the envelope must not race the pump's reads
	// (a premature close surfaces to the client as "http2: response body
	// closed" / a truncated stream).
	if req.StreamID != "" {
		sid := req.StreamID
		go func() {
			err := pumpCleanedSSE(upstream.Body, sseFramed, func(p []byte) error { return streamEmit(sid, p) })
			upstream.Body.Close()
			if err != nil {
				streamEmitError(sid, err.Error())
			}
			_ = streamClose(sid)
		}()
		return okEnvelope(streamResponse{Headers: headers}), nil
	}

	// Sync fallback: collect one cleaned chunk per data event and hand them
	// back in a single reply.
	var chunks []pluginapi.ExecutorStreamChunk
	if _, err := walkCleanedSSE(upstream.Body, sseFramed, func(p string) error {
		chunks = append(chunks, pluginapi.ExecutorStreamChunk{Payload: []byte(p)})
		return nil
	}); err != nil {
		upstream.Body.Close()
		return nil, err
	}
	upstream.Body.Close()
	return okEnvelope(streamResponse{Headers: headers, Chunks: chunks}), nil
}

// streamHeaders matches the other plugins' response headers: the host streams
// the client response as SSE, so Content-Type must be text/event-stream (not
// the upstream's own headers, which may carry length/framing mismatches).
func streamHeaders() http.Header {
	h := http.Header{}
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	return h
}

// clientNeedsSSEFrame reports whether the host passthrough wants payloads
// pre-framed as "data: " lines. OpenAI chat/completions requests do NOT:
// the host frames bare JSON payloads itself.
func clientNeedsSSEFrame(metadata map[string]any) bool {
	path, _ := metadata["request_path"].(string)
	switch strings.ToLower(strings.TrimSpace(path)) {
	case "/v1/chat/completions", "/v1/completions":
		return false
	default:
		return true
	}
}

// walkCleanedSSE reads an upstream SSE stream and emits one payload per data
// event: bare JSON (or a "data: " line when sseFramed), with keep-alive
// comments, empty shells, and the trailing [DONE] dropped — the host appends
// its own stream terminator. Returns an error for a mid-stream read failure or
// an empty stream (no completion payload seen).
func walkCleanedSSE(r io.Reader, sseFramed bool, emit func(string) error) (bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	seenPayload := false
	for sc.Scan() {
		content, meaningful, _ := upstreamSSEFrame(sc.Text())
		seenPayload = seenPayload || meaningful
		if content == "" || content == "[DONE]" {
			continue
		}
		cleaned := cleanChunkJSON(content)
		if cleaned == "" {
			continue
		}
		if sseFramed {
			cleaned = "data: " + cleaned
		}
		if err := emit(cleaned); err != nil {
			return seenPayload, err
		}
	}
	if err := sc.Err(); err != nil {
		return seenPayload, err
	}
	if !seenPayload {
		return seenPayload, errors.New("upstream stream ended without a completion payload")
	}
	return seenPayload, nil
}

func pumpCleanedSSE(r io.Reader, sseFramed bool, emit func([]byte) error) error {
	_, err := walkCleanedSSE(r, sseFramed, func(p string) error { return emit([]byte(p)) })
	return err
}

// upstreamSSEFrame extracts the payload of one upstream SSE line: content is
// the JSON after "data:", meaningful reports whether the line was a data event
// (the empty-stream guard counts only those), and comment/blank/event lines
// carry no payload.
func upstreamSSEFrame(line string) (string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ":") {
		return "", false, nil
	}
	if strings.HasPrefix(trimmed, "data:") {
		return strings.TrimSpace(strings.TrimPrefix(trimmed, "data:")), true, nil
	}
	return "", false, nil
}

// cleanChunkJSON strips known-problematic empty shells from choice deltas so
// strict clients do not misread a terminal chunk as a truncated tool call.
// Mirrors the workbuddy plugin's cleanChunkJSON behaviour.
func cleanChunkJSON(s string) string {
	if strings.HasPrefix(s, ":") {
		return ""
	}
	var obj map[string]any
	if json.Unmarshal([]byte(s), &obj) != nil {
		return s
	}
	changed := false
	if choices, ok := obj["choices"].([]any); ok {
		for _, c := range choices {
			choice, ok := c.(map[string]any)
			if !ok {
				continue
			}
			delta, ok := choice["delta"].(map[string]any)
			if !ok {
				continue
			}
			if v, present := delta["function_call"]; present && isEmptyValue(v) {
				delete(delta, "function_call")
				changed = true
			}
			if v, present := delta["tool_calls"]; present {
				if arr, isArr := v.([]any); isArr && len(arr) == 0 {
					delete(delta, "tool_calls")
					changed = true
				}
			}
			for _, noise := range []string{"extra_fields", "refusal", "reasoning_content"} {
				if v, present := delta[noise]; present && isEmptyValue(v) {
					delete(delta, noise)
					changed = true
				}
			}
			if len(delta) == 0 {
				if fr, _ := choice["finish_reason"].(string); fr == "" {
					return ""
				}
			}
		}
	}
	if !changed {
		return s
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return s
	}
	return string(out)
}

func isEmptyValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case string:
		return t == ""
	case []any:
		return len(t) == 0
	case map[string]any:
		return len(t) == 0
	case bool:
		return !t
	case float64:
		return t == 0
	}
	return false
}

// aggregateCompletion folds an OpenAI-shaped SSE into a single chat.completion
// object for the non-stream executor path.
func aggregateCompletion(r io.Reader, model string) ([]byte, error) {
	type chatDelta struct {
		Role    string `json:"role,omitempty"`
		Content string `json:"content,omitempty"`
		Reason  string `json:"reasoning_content,omitempty"`
	}
	type chatChunk struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Created int64  `json:"created"`
		Choices []struct {
			Index        int       `json:"index"`
			Delta        chatDelta `json:"delta"`
			FinishReason *string   `json:"finish_reason"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens int `json:"prompt_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	}
	var (
		id, respModel   string
		created         int64
		content, reason strings.Builder
		// role is single-valued: the upstream repeats "role":"assistant" in
		// every delta frame, so it must be taken once rather than accumulated
		// (the previous strings.Builder produced "assistantassistant...").
		role   string
		finish string
		usage  *chatChunk
	)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 256*1024), 8*1024*1024)
	lastSeen := time.Now()
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" || payload == "" {
			continue
		}
		var ch chatChunk
		if err := json.Unmarshal([]byte(payload), &ch); err != nil {
			continue
		}
		lastSeen = time.Now()
		if ch.ID != "" {
			id = ch.ID
		}
		if ch.Model != "" {
			respModel = ch.Model
		}
		if ch.Created > 0 {
			created = ch.Created
		}
		if len(ch.Choices) > 0 {
			d := ch.Choices[0].Delta
			if d.Role != "" && role == "" {
				role = d.Role
			}
			content.WriteString(d.Content)
			reason.WriteString(d.Reason)
			if ch.Choices[0].FinishReason != nil && *ch.Choices[0].FinishReason != "" {
				finish = *ch.Choices[0].FinishReason
			}
		}
		if ch.Usage != nil {
			usage = &ch
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	_ = lastSeen
	if id == "" {
		id = "cmb-" + randomSuffix()
	}
	if respModel == "" {
		respModel = model
	}
	if created == 0 {
		created = time.Now().Unix()
	}
	if finish == "" {
		finish = "stop"
	}
	if role == "" {
		role = "assistant"
	}
	message := map[string]any{
		"role":    role,
		"content": content.String(),
	}
	if reason.Len() > 0 {
		message["reasoning_content"] = reason.String()
	}
	body := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": created,
		"model":   respModel,
		"choices": []any{map[string]any{
			"index":         0,
			"message":       message,
			"finish_reason": finish,
		}},
	}
	if usage != nil {
		u := usage
		if u.Usage != nil {
			body["usage"] = map[string]any{
				"prompt_tokens":     u.Usage.PromptTokens,
				"completion_tokens": u.Usage.TotalTokens - u.Usage.PromptTokens,
				"total_tokens":      u.Usage.TotalTokens,
			}
		}
	}
	return json.Marshal(body)
}

// randomSuffix makes a non-empty opaque completion id without crypto deps.
func randomSuffix() string {
	var b bytes.Buffer
	now := time.Now().UnixNano()
	for i := 0; i < 8; i++ {
		b.WriteByte("abcdef0123456789"[now&0xf])
		now >>= 4
	}
	return b.String()
}
