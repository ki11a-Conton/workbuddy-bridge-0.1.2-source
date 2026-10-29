// workbuddy-ai — CLIProxyAPI plugin serving the INTERNATIONAL WorkBuddy product
// (region=global / https://www.workbuddy.ai / "WorkBuddy AI" desktop identity).
//
// Ported wire facts come from zlZayn/dsh-workbuddy-bridge (MIT) and were
// verified live by D:\toolchain\spike-workbuddy-ai: the chat endpoint answers
// OpenAI-shaped SSE (data: {...} / [DONE]) so the executor forwards it instead
// of translating; the /v3/config catalog is split by the app-shaped User-Agent.
//
// The plugin is deliberately independent from the cpa-multi-plugins workbuddy
// plugin: it owns `workbuddy-ai-*` auth files (adopted copies of the global
// accounts) and exposes itself as provider `workbuddy-ai` for A/B comparison.
//
// Built with -buildmode=c-shared and exports the cliproxy C ABI entry points.
package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
        void* ptr;
        size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
        uint32_t abi_version;
        void* host_ctx;
        cliproxy_host_call_fn call;
        cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
        uint32_t abi_version;
        cliproxy_plugin_call_fn call;
        cliproxy_plugin_free_fn free_buffer;
        cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

static int wbai_call_host(cliproxy_host_api* api, const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
        return api->call(api->host_ctx, method, request, request_len, response);
}
static void wbai_free_host_buffer(cliproxy_host_api* api, void* ptr, size_t len) {
        api->free_buffer(ptr, len);
}

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

const (
	// providerName is the CPA-facing provider id. The client-facing model name
	// becomes "<provider>/deepseek-v4.1-flash"; "workbuddy-bridge" names the
	// dsh-workbuddy-bridge origin of this port while staying clearly distinct
	// from the cpa-multi-plugins "workbuddy" plugin.
	providerName  = "workbuddy-bridge"
	pluginVersion = "0.1.5"
	// pluginLogo is the WorkBuddy AI brand mark served by the management UI.
	pluginLogo = ""
)

var hostAPI *C.cliproxy_host_api // captured at init, used for async host calls

func main() {}

// -----------------------------------------------------------------------------
// C ABI exports
// -----------------------------------------------------------------------------

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	hostAPI = host
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr = nil
		response.len = 0
	}
	if method == nil {
		writeResponse(response, errorEnvelope("invalid_method", "method is required"))
		return 1
	}
	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, errHandle := handleMethod(C.GoString(method), requestBytes)
	if errHandle != nil {
		writeResponse(response, errorEnvelopeFor(errHandle))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, len C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	// No-op on purpose: the host dlclose()es this library immediately after this
	// call, and touching any Go runtime state here risks a cgo SIGSEGV.
}

// -----------------------------------------------------------------------------
// Host calls
// -----------------------------------------------------------------------------

// hostCall invokes a host RPC method via the function-pointer table captured at
// init (stream emit/close, etc.).
func hostCall(method string, request []byte) ([]byte, error) {
	if hostAPI == nil || hostAPI.call == nil {
		return nil, errors.New("host API unavailable")
	}
	var reqPtr unsafe.Pointer
	if len(request) > 0 {
		reqPtr = C.CBytes(request)
		defer C.free(reqPtr)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var resp C.cliproxy_buffer
	ret := C.wbai_call_host(hostAPI, cMethod, (*C.uint8_t)(reqPtr), C.size_t(len(request)), &resp)
	if ret != 0 {
		return nil, fmt.Errorf("host call %s returned %d", method, ret)
	}
	if resp.ptr == nil || resp.len == 0 {
		return nil, nil
	}
	defer C.wbai_free_host_buffer(hostAPI, resp.ptr, resp.len)
	return C.GoBytes(resp.ptr, C.int(resp.len)), nil
}

// streamEmit pushes one chunk payload to the host stream (async streaming).
func streamEmit(streamID string, payload []byte) error {
	body, _ := json.Marshal(map[string]any{"stream_id": streamID, "payload": payload})
	_, err := hostCall(pluginabi.MethodHostStreamEmit, body)
	return err
}

// streamEmitError pushes a terminal error to the host stream. The message must
// travel in the RPC top-level "error" field, never inside "payload": the host
// turns req.Error into the chunk's Err (feeding its failure-classification
// layer and surfacing a real terminal error to the client), while a
// payload-embedded {"error":...} blob is just another data chunk.
func streamEmitError(streamID, message string) {
	if streamID == "" {
		return
	}
	body, _ := json.Marshal(map[string]any{"stream_id": streamID, "error": message})
	_, _ = hostCall(pluginabi.MethodHostStreamEmit, body)
}

// streamClose closes an async host stream.
func streamClose(streamID string) error {
	body, _ := json.Marshal(map[string]any{"stream_id": streamID})
	_, err := hostCall(pluginabi.MethodHostStreamClose, body)
	return err
}

// -----------------------------------------------------------------------------
// RPC envelopes
// -----------------------------------------------------------------------------

type envelopeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// HTTPStatus mirrors pluginabi.Error.HTTPStatus: the host's envelope decode
	// feeds it into rpcError.StatusCode(), which MarkResult uses for the
	// per-status credential cooldown (402 -> 30m, 429 -> quota backoff,
	// 401 -> 30m). Without it every plugin failure looks status-less to the
	// host, so a request-shaped 11128 rejection cools the credential and the
	// next call dies with 503 auth_unavailable.
	HTTPStatus int `json:"http_status,omitempty"`
}

type rpcEnvelope struct {
	OK     bool           `json:"ok"`
	Result any            `json:"result,omitempty"`
	Error  *envelopeError `json:"error,omitempty"`
}

func okEnvelope(result any) []byte {
	raw, _ := json.Marshal(rpcEnvelope{OK: true, Result: result})
	return raw
}

func errorEnvelope(code, message string) []byte {
	raw, _ := json.Marshal(rpcEnvelope{OK: false, Error: &envelopeError{Code: code, Message: message}})
	return raw
}

// errorEnvelopeFor serializes a handler error, preserving the upstream HTTP
// status when the error carries one (statusError / any StatusCode() impl). The
// status crosses the RPC boundary as envelope error http_status and drives the
// host's real per-status credential cooldown — see upstreamStatusError for the
// account-level vs request-level policy.
func errorEnvelopeFor(err error) []byte {
	var sc interface{ StatusCode() int }
	if errors.As(err, &sc) && sc.StatusCode() > 0 {
		raw, _ := json.Marshal(rpcEnvelope{OK: false, Error: &envelopeError{
			Code: "plugin_error", Message: err.Error(), HTTPStatus: sc.StatusCode(),
		}})
		return raw
	}
	return errorEnvelope("internal", err.Error())
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}

// -----------------------------------------------------------------------------
// Registration
// -----------------------------------------------------------------------------

type registrationCapability struct {
	ModelProvider         bool                         `json:"model_provider"`
	AuthProvider          bool                         `json:"auth_provider"`
	FrontendAuthProvider  bool                         `json:"frontend_auth_provider"`
	Executor              bool                         `json:"executor"`
	ExecutorModelScope    pluginapi.ExecutorModelScope `json:"executor_model_scope"`
	ExecutorInputFormats  []string                     `json:"executor_input_formats,omitempty"`
	ExecutorOutputFormats []string                     `json:"executor_output_formats,omitempty"`
	Scheduler             bool                         `json:"scheduler"`
	ManagementAPI         bool                         `json:"management_api"`
	UsagePlugin           bool                         `json:"usage_plugin"`
}

type registration struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Metadata      pluginapi.Metadata     `json:"metadata"`
	Capabilities  registrationCapability `json:"capabilities"`
}

func wbaiRegistration() registration {
	return registration{
		SchemaVersion: pluginabi.SchemaVersion,
		Metadata: pluginapi.Metadata{
			Name:             providerName,
			Version:          pluginVersion,
			Author:           "killa-conton, zlZayn, Corrine Hu (wire ported from dsh-workbuddy-bridge, MIT)",
			GitHubRepository: "https://github.com/zlZayn/dsh-workbuddy-bridge",
			Logo:             pluginLogo,
			ConfigFields: []pluginapi.ConfigField{
				{Name: "app_version", Type: pluginapi.ConfigFieldTypeString, Description: "WorkBuddy AI desktop app version embedded in the chat User-Agent (default 5.5.2)."},
			},
		},
		Capabilities: registrationCapability{
			ModelProvider:         true,
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    pluginapi.ExecutorModelScopeOAuth,
			ExecutorInputFormats:  []string{"chat-completions"},
			ExecutorOutputFormats: []string{"chat-completions"},
			Scheduler:             false,
			ManagementAPI:         true,
			UsagePlugin:           false,
		},
	}
}

// identifierResponse satisfies *identifier RPCs for auth and executor.
type identifierResponse struct {
	Identifier string `json:"identifier"`
}

// executorStreamRequest wraps the host's executor.execute_stream RPC payload:
// the ExecutorRequest plus the async stream id the host uses to receive chunks.
type executorStreamRequest struct {
	pluginapi.ExecutorRequest
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

// streamResponse is the executor.execute_stream result; async streams return no
// chunks and pump via host.stream.emit instead.
type streamResponse struct {
	Headers http.Header                     `json:"headers,omitempty"`
	Chunks  []pluginapi.ExecutorStreamChunk `json:"chunks,omitempty"`
}

// -----------------------------------------------------------------------------
// Method dispatch
// -----------------------------------------------------------------------------

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case pluginabi.MethodPluginRegister, pluginabi.MethodPluginReconfigure:
		return okEnvelope(wbaiRegistration()), nil
	case pluginabi.MethodModelStatic:
		return handleModelStatic(request)
	case pluginabi.MethodModelForAuth:
		return handleModelForAuth(request)
	case pluginabi.MethodAuthIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerName}), nil
	case pluginabi.MethodAuthParse:
		return handleParseAuth(request)
	case pluginabi.MethodAuthLoginStart:
		return errorEnvelope("unsupported", "workbuddy-ai login: use a real WorkBuddy AI account (import the desktop/Cli credential file)"), nil
	case pluginabi.MethodAuthLoginPoll:
		return errorEnvelope("unsupported", "workbuddy-ai has no pollable login flow (adopt a credential file instead)"), nil
	case pluginabi.MethodAuthRefresh:
		return handleRefreshAuth(request)
	case pluginabi.MethodExecutorIdentifier:
		return okEnvelope(identifierResponse{Identifier: providerName}), nil
	case pluginabi.MethodExecutorExecute:
		return handleExecExecute(request)
	case pluginabi.MethodExecutorExecuteStream:
		return handleExecStream(request)
	case pluginabi.MethodExecutorCountTokens:
		return okEnvelope(pluginapi.ExecutorResponse{Payload: []byte(`{"input_tokens":0}`)}), nil
	case pluginabi.MethodManagementRegister:
		return okEnvelope(managementRegistration()), nil
	case pluginabi.MethodManagementHandle:
		return handleManagement(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method), nil
	}
}
