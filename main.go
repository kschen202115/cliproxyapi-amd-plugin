// SPDX-License-Identifier: MIT
// AMD Radeon Cloud provider for CLIProxyAPI's native plugin ABI.
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

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unsafe"
)

const (
	abiVersion    uint32 = 1
	schemaVersion uint32 = 6
	provider             = "amd"
	baseURL              = "https://developer.amd.com.cn/radeon/api/v1"
	usageURL             = "https://radeon-global.anruicloud.com/api/profile/model-usage"
)

type envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

type registration struct {
	SchemaVersion uint32       `json:"schema_version"`
	Metadata      metadata     `json:"metadata"`
	Capabilities  capabilities `json:"capabilities"`
}

type metadata struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository"`
	ConfigFields     []configField `json:"ConfigFields"`
}

type configField struct {
	Name        string `json:"Name"`
	Type        string `json:"Type"`
	Description string `json:"Description"`
}

type capabilities struct {
	ModelProvider         bool     `json:"model_provider"`
	AuthProvider          bool     `json:"auth_provider"`
	Executor              bool     `json:"executor"`
	ExecutorModelScope    string   `json:"executor_model_scope"`
	ExecutorInputFormats  []string `json:"executor_input_formats"`
	ExecutorOutputFormats []string `json:"executor_output_formats"`
	QuotaProvider         bool     `json:"quota_provider"`
	ManagementAPI         bool     `json:"management_api"`
}

type identifierResponse struct {
	Identifier string `json:"identifier"`
}

// Credential is stored verbatim in CLIProxyAPI's auth directory. It intentionally
// contains only the AMD API key and a non-secret optional display label.
type credential struct {
	Provider string `json:"provider,omitempty"`
	Type     string `json:"type,omitempty"`
	APIKey   string `json:"api_key"`
	Label    string `json:"label,omitempty"`
	ID       string `json:"id,omitempty"`
}

type authParseRequest struct {
	Provider string `json:"Provider"`
	Path     string `json:"Path"`
	FileName string `json:"FileName"`
	RawJSON  []byte `json:"RawJSON"`
}

type authRefreshRequest struct {
	AuthID      string            `json:"AuthID"`
	StorageJSON []byte            `json:"StorageJSON"`
	Metadata    map[string]any    `json:"Metadata"`
	Attributes  map[string]string `json:"Attributes"`
}

type authData struct {
	Provider    string         `json:"Provider"`
	ID          string         `json:"ID"`
	FileName    string         `json:"FileName"`
	Label       string         `json:"Label"`
	StorageJSON []byte         `json:"StorageJSON"`
	Metadata    map[string]any `json:"Metadata,omitempty"`
}

type authParseResponse struct {
	Handled bool     `json:"Handled"`
	Auth    authData `json:"Auth,omitempty"`
}

type authRefreshResponse struct {
	Auth authData `json:"Auth"`
}

type authLoginStartRequest struct {
	BaseURL string `json:"BaseURL"`
}

type authLoginStartResponse struct {
	Provider  string `json:"Provider"`
	URL       string `json:"URL"`
	State     string `json:"State"`
	ExpiresAt string `json:"ExpiresAt"`
}

type authLoginPollRequest struct {
	State string `json:"State"`
}

type authLoginPollResponse struct {
	Status  string   `json:"Status"`
	Message string   `json:"Message"`
	Auth    authData `json:"Auth,omitempty"`
}

type authModelRequest struct {
	AuthID         string `json:"AuthID"`
	AuthProvider   string `json:"AuthProvider"`
	StorageJSON    []byte `json:"StorageJSON"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type modelResponse struct {
	Provider string      `json:"Provider"`
	Models   []modelInfo `json:"Models"`
}

type modelInfo struct {
	ID                         string   `json:"ID"`
	Object                     string   `json:"Object"`
	OwnedBy                    string   `json:"OwnedBy"`
	DisplayName                string   `json:"DisplayName"`
	SupportedGenerationMethods []string `json:"SupportedGenerationMethods"`
	ContextLength              int64    `json:"ContextLength,omitempty"`
	UserDefined                bool     `json:"UserDefined"`
}

type amdModelList struct {
	Data []amdModel `json:"data"`
}

type amdModel struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ContextLength int64  `json:"context_length"`
}

type executorRequest struct {
	AuthID         string `json:"AuthID"`
	AuthProvider   string `json:"AuthProvider"`
	Model          string `json:"Model"`
	Format         string `json:"Format"`
	Stream         bool   `json:"Stream"`
	Payload        []byte `json:"Payload"`
	StorageJSON    []byte `json:"StorageJSON"`
	StreamID       string `json:"stream_id,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type executorResponse struct {
	Payload []byte              `json:"Payload"`
	Headers map[string][]string `json:"Headers,omitempty"`
}

type executorStreamResponse struct {
	Headers map[string][]string `json:"headers,omitempty"`
}

type hostHTTPRequest struct {
	HostCallbackID string              `json:"host_callback_id,omitempty"`
	Method         string              `json:"method"`
	URL            string              `json:"url"`
	Headers        map[string][]string `json:"headers,omitempty"`
	Body           []byte              `json:"body,omitempty"`
}

type hostHTTPResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}

type hostStreamStartResponse struct {
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	StreamID   string              `json:"stream_id"`
}

type hostStreamReadResponse struct {
	Payload []byte `json:"payload,omitempty"`
	Error   string `json:"error,omitempty"`
	Done    bool   `json:"done,omitempty"`
}

type streamReadRequest struct {
	StreamID string `json:"stream_id"`
}

type streamEmitRequest struct {
	StreamID string `json:"stream_id"`
	Payload  []byte `json:"payload,omitempty"`
	Error    string `json:"error,omitempty"`
}

type streamCloseRequest struct {
	StreamID string `json:"stream_id"`
	Error    string `json:"error,omitempty"`
}

type managementRegistration struct {
	Routes    []managementRoute    `json:"routes,omitempty"`
	Resources []managementResource `json:"resources,omitempty"`
}

type managementRoute struct {
	Method string `json:"Method"`
	Path   string `json:"Path"`
}

type managementResource struct {
	Path        string `json:"Path"`
	Menu        string `json:"Menu"`
	Description string `json:"Description"`
}

type managementRequest struct {
	Method string              `json:"Method"`
	Path   string              `json:"Path"`
	Query  map[string][]string `json:"Query"`
	Body   []byte              `json:"Body"`
}

type managementResponse struct {
	StatusCode int                 `json:"StatusCode"`
	Headers    map[string][]string `json:"Headers"`
	Body       []byte              `json:"Body"`
}

type apiKeyForm struct {
	APIKey string `json:"api_key"`
	Label  string `json:"label"`
	State  string `json:"state"`
}

type hostAuthListResponse struct {
	Files []hostAuthFile `json:"files"`
}

type hostAuthFile struct {
	AuthIndex string `json:"auth_index"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
}

type hostAuthGetResponse struct {
	Name string          `json:"name"`
	JSON json.RawMessage `json:"json"`
}

type quotaFetchRequest struct {
	Provider       string `json:"provider"`
	StorageJSON    []byte `json:"storage_json,omitempty"`
	HostCallbackID string `json:"host_callback_id,omitempty"`
}

type quotaDescribeResponse struct {
	SupportedProviders []string `json:"supported_providers"`
	DisplayName        string   `json:"display_name"`
	SupportsReset      bool     `json:"supports_reset"`
}

type quotaMetric struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Value    float64 `json:"value"`
	Unit     string  `json:"unit,omitempty"`
	Format   string  `json:"format,omitempty"`
	Currency string  `json:"currency,omitempty"`
}

type quotaBucket struct {
	Window            string  `json:"window,omitempty"`
	RemainingFraction float64 `json:"remainingFraction"`
	ResetTime         string  `json:"resetTime,omitempty"`
	Description       string  `json:"description,omitempty"`
}

type quotaGroup struct {
	DisplayName string        `json:"displayName,omitempty"`
	Buckets     []quotaBucket `json:"buckets,omitempty"`
}

type quotaFetchResponse struct {
	Summary []quotaMetric `json:"summary,omitempty"`
	Groups  []quotaGroup  `json:"groups,omitempty"`
}

type amdUsageProfile struct {
	Status                string  `json:"status"`
	RPMLimit              float64 `json:"rpm_limit"`
	DailyCostLimitUSD     float64 `json:"daily_cost_limit_usd"`
	DailyCostUsedUSD      float64 `json:"daily_cost_used_usd"`
	DailyCostRemainingUSD float64 `json:"daily_cost_remaining_usd"`
	DailyResetAt          string  `json:"daily_reset_at"`
	Today                 struct {
		Requests    float64 `json:"requests"`
		Errors      float64 `json:"errors"`
		TotalTokens float64 `json:"total_tokens"`
	} `json:"today"`
}

type upstreamStatusError struct {
	status  int
	message string
}

func (e upstreamStatusError) Error() string   { return e.message }
func (e upstreamStatusError) StatusCode() int { return e.status }

func main() {}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil || host == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(abiVersion)
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
		writeResponse(response, errorEnvelope("invalid_method", "method is required", 400))
		return 1
	}

	var requestBytes []byte
	if request != nil && requestLen > 0 {
		requestBytes = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	raw, err := handleMethod(C.GoString(method), requestBytes)
	if err != nil {
		status := 0
		var statusErr interface{ StatusCode() int }
		if errors.As(err, &statusErr) {
			status = statusErr.StatusCode()
		}
		writeResponse(response, errorEnvelope("amd_upstream_error", err.Error(), status))
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, length C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
	_ = length
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {}

func handleMethod(method string, request []byte) ([]byte, error) {
	switch method {
	case "plugin.register", "plugin.reconfigure":
		return okEnvelope(pluginRegistration())
	case "auth.identifier", "executor.identifier":
		return okEnvelope(identifierResponse{Identifier: provider})
	case "quota.identifier":
		return okEnvelope(identifierResponse{Identifier: provider})
	case "auth.parse":
		return parseAuth(request)
	case "auth.refresh":
		return refreshAuth(request)
	case "auth.login.start":
		return startAPIKeyLogin(request)
	case "auth.login.poll":
		return pollAPIKeyLogin(request)
	case "model.static":
		// Radeon exposes one public catalog for all keys. Discover it per auth record
		// so an invalid/expired key never makes models look executable.
		return okEnvelope(modelResponse{Provider: provider, Models: []modelInfo{}})
	case "model.for_auth":
		return discoverModels(request)
	case "executor.execute":
		return execute(request)
	case "executor.execute_stream":
		return executeStream(request)
	case "executor.count_tokens":
		return errorEnvelope("unsupported", "AMD Radeon Cloud's shared Chat Completions endpoint does not expose a token-count endpoint", 400), nil
	case "executor.http_request":
		return errorEnvelope("unsupported", "AMD executor only supports model execution", 400), nil
	case "quota.describe":
		return okEnvelope(quotaDescribeResponse{SupportedProviders: []string{provider}, DisplayName: "AMD Radeon Cloud", SupportsReset: false})
	case "quota.fetch":
		return fetchQuota(request)
	case "quota.reset":
		return okEnvelope(map[string]any{"success": false, "message": "AMD Radeon Cloud does not support quota reset"})
	case "management.register":
		return okEnvelope(managementRegistration{
			Routes: []managementRoute{{Method: "POST", Path: "/plugins/amd/credentials"}},
			Resources: []managementResource{{
				Path:        "/credentials",
				Menu:        "AMD Radeon Cloud",
				Description: "Add an AMD Radeon Cloud API key.",
			}},
		})
	case "management.handle":
		return handleManagement(request)
	default:
		return errorEnvelope("unknown_method", "unknown method: "+method, 400), nil
	}
}

func pluginRegistration() registration {
	return registration{
		SchemaVersion: schemaVersion,
		Metadata: metadata{
			Name:             provider,
			Version:          "0.1.1",
			Author:           "AMD AIM community",
			GitHubRepository: "https://amd-aim.github.io/radeon-cloud-docs/",
			ConfigFields:     []configField{},
		},
		Capabilities: capabilities{
			ModelProvider:         true,
			AuthProvider:          true,
			Executor:              true,
			ExecutorModelScope:    "oauth",
			ExecutorInputFormats:  []string{"chat-completions"},
			ExecutorOutputFormats: []string{"chat-completions"},
			QuotaProvider:         true,
			ManagementAPI:         true,
		},
	}
}

func startAPIKeyLogin(raw []byte) ([]byte, error) {
	var req authLoginStartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD login request: %w", err)
	}
	state := fmt.Sprintf("amd-api-key-%d", time.Now().UTC().UnixNano())
	resourceURL := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	resourceURL = strings.TrimSuffix(resourceURL, "/v0/management")
	resourceURL += "/v0/resource/plugins/amd/credentials?state=" + url.QueryEscape(state)
	return okEnvelope(authLoginStartResponse{
		Provider:  provider,
		URL:       resourceURL,
		State:     state,
		ExpiresAt: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339),
	})
}

func pollAPIKeyLogin(raw []byte) ([]byte, error) {
	var req authLoginPollRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD login poll: %w", err)
	}
	state := strings.TrimSpace(req.State)
	if !strings.HasPrefix(state, "amd-api-key-") {
		return okEnvelope(authLoginPollResponse{Status: "error", Message: "invalid AMD API-key login state"})
	}
	result, err := hostCall("host.auth.list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var list hostAuthListResponse
	if err := json.Unmarshal(result, &list); err != nil {
		return nil, fmt.Errorf("decode AMD auth list: %w", err)
	}
	target := state + ".json"
	for _, item := range list.Files {
		if item.Name != target || !isAMDProvider(item.Provider) || item.AuthIndex == "" {
			continue
		}
		result, err := hostCall("host.auth.get", map[string]string{"auth_index": item.AuthIndex})
		if err != nil {
			return nil, err
		}
		var saved hostAuthGetResponse
		if err := json.Unmarshal(result, &saved); err != nil {
			return nil, fmt.Errorf("decode saved AMD credential: %w", err)
		}
		cred, explicit, err := decodeCredential(saved.JSON)
		if err != nil || !explicit {
			return okEnvelope(authLoginPollResponse{Status: "error", Message: "saved AMD credential could not be read"})
		}
		return okEnvelope(authLoginPollResponse{
			Status:  "success",
			Message: "AMD API key saved",
			Auth:    makeAuthData(cred, saved.JSON, target, state),
		})
	}
	return okEnvelope(authLoginPollResponse{Status: "pending", Message: "Enter your Radeon API key in the opened page."})
}

func handleManagement(raw []byte) ([]byte, error) {
	var req managementRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD management request: %w", err)
	}
	if strings.EqualFold(req.Method, "GET") && strings.HasSuffix(req.Path, "/credentials") {
		return okEnvelope(htmlManagementResponse(apiKeyPage(firstQuery(req.Query, "state"))))
	}
	if !strings.EqualFold(req.Method, "POST") || !strings.HasSuffix(req.Path, "/plugins/amd/credentials") {
		return okEnvelope(jsonManagementResponse(404, map[string]string{"error": "not_found"}))
	}
	var form apiKeyForm
	if err := json.Unmarshal(req.Body, &form); err != nil {
		return okEnvelope(jsonManagementResponse(400, map[string]string{"error": "invalid_request", "message": "invalid JSON body"}))
	}
	key := strings.TrimSpace(form.APIKey)
	state := strings.TrimSpace(form.State)
	if !strings.HasPrefix(key, "rc-") || len(key) < 10 {
		return okEnvelope(jsonManagementResponse(400, map[string]string{"error": "invalid_api_key", "message": "Enter a Radeon API key beginning with rc-"}))
	}
	if state == "" || !strings.HasPrefix(state, "amd-api-key-") {
		state = fmt.Sprintf("amd-api-key-%d", time.Now().UTC().UnixNano())
	}
	label := strings.TrimSpace(form.Label)
	if label == "" {
		label = "AMD Radeon Cloud"
	}
	storage, err := json.Marshal(credential{Provider: provider, APIKey: key, Label: label, ID: state})
	if err != nil {
		return nil, err
	}
	if _, err := hostCall("host.auth.save", map[string]any{
		"name": state + ".json",
		"json": json.RawMessage(storage),
	}); err != nil {
		return nil, err
	}
	return okEnvelope(jsonManagementResponse(201, map[string]string{"status": "ok", "message": "AMD API key saved"}))
}

func firstQuery(values map[string][]string, name string) string {
	if values == nil || len(values[name]) == 0 {
		return ""
	}
	return strings.TrimSpace(values[name][0])
}

func jsonManagementResponse(status int, body any) managementResponse {
	raw, _ := json.Marshal(body)
	return managementResponse{StatusCode: status, Headers: map[string][]string{"content-type": {"application/json"}}, Body: raw}
}

func htmlManagementResponse(body []byte) managementResponse {
	return managementResponse{
		StatusCode: 200,
		Headers: map[string][]string{
			"content-type":            {"text/html; charset=utf-8"},
			"content-security-policy": {"default-src 'self'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; base-uri 'none'; form-action 'self'"},
		},
		Body: body,
	}
}

func apiKeyPage(state string) []byte {
	stateJSON, _ := json.Marshal(state)
	return []byte(`<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>AMD Radeon Cloud API Key</title><style>body{font:15px system-ui,sans-serif;max-width:620px;margin:40px auto;padding:0 20px;color:#18212f}label{display:block;font-weight:600;margin-top:16px}input{box-sizing:border-box;width:100%;margin-top:6px;padding:10px;border:1px solid #b8c2cf;border-radius:7px}button{margin-top:22px;padding:10px 16px;border:0;border-radius:7px;background:#d64b2a;color:white;font-weight:700;cursor:pointer}#result{margin-top:16px;white-space:pre-wrap}.hint{color:#526273;font-size:13px}</style><main><h1>添加 AMD Radeon Cloud API Key</h1><p class="hint">密钥仅发送到当前 CLIProxyAPI 服务，用于创建一个 amd 凭证文件。</p><form id="form"><label>Radeon API Key<input id="key" type="password" autocomplete="off" placeholder="rc-..." required></label><label>名称（可选）<input id="label" maxlength="80" placeholder="AMD Radeon Cloud"></label><details><summary>管理密钥（仅在自动读取失败时填写）</summary><input id="managementKey" type="password" autocomplete="off" placeholder="CLIProxyAPI management key"></details><button>保存 API Key</button></form><p id="result" role="status"></p></main><script>const state=` + string(stateJSON) + `;const result=document.getElementById('result');function storedKey(){const direct=['managementKey','management-key','management_password','managementPassword'];for(const k of direct){const v=localStorage.getItem(k);if(v)return v}for(let i=0;i<localStorage.length;i++){const k=localStorage.key(i)||'';if(/management.*(key|password)|(key|password).*management/i.test(k)){const v=localStorage.getItem(k);if(v)return v}}return ''}document.getElementById('form').addEventListener('submit',async e=>{e.preventDefault();result.textContent='保存中…';const key=document.getElementById('key').value.trim();const label=document.getElementById('label').value.trim();const managementKey=document.getElementById('managementKey').value.trim()||storedKey();if(!managementKey){result.textContent='未找到管理密钥。请展开并填写 CLIProxyAPI 管理密钥。';return}try{const r=await fetch('/v0/management/plugins/amd/credentials',{method:'POST',headers:{'Content-Type':'application/json','Authorization':'Bearer '+managementKey},body:JSON.stringify({api_key:key,label,state})});const data=await r.json();if(!r.ok)throw new Error(data.message||data.error||'保存失败');document.getElementById('key').value='';result.textContent='已保存。请返回认证窗口，它会自动完成；额度和模型将随后加载。'}catch(err){result.textContent='保存失败：'+err.message}});</script></html>`)
}

func parseAuth(raw []byte) ([]byte, error) {
	var req authParseRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD auth parse request: %w", err)
	}
	cred, explicit, err := decodeCredential(req.RawJSON)
	if !explicit {
		// This file is not recognisably ours, so another auth provider may parse it.
		return okEnvelope(authParseResponse{Handled: false})
	}
	if err != nil {
		return nil, upstreamStatusError{status: 400, message: "AMD credential is invalid or missing api_key"}
	}
	if strings.TrimSpace(req.Provider) != "" && !isAMDProvider(req.Provider) {
		return okEnvelope(authParseResponse{Handled: false})
	}
	auth := makeAuthData(cred, req.RawJSON, req.FileName, "")
	return okEnvelope(authParseResponse{Handled: true, Auth: auth})
}

func refreshAuth(raw []byte) ([]byte, error) {
	var req authRefreshRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD auth refresh request: %w", err)
	}
	cred, _, err := decodeCredential(req.StorageJSON)
	if err != nil {
		return nil, upstreamStatusError{status: 401, message: "AMD credential is invalid or missing api_key"}
	}
	return okEnvelope(authRefreshResponse{Auth: makeAuthData(cred, req.StorageJSON, "", req.AuthID)})
}

func discoverModels(raw []byte) ([]byte, error) {
	var req authModelRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD model discovery request: %w", err)
	}
	if !isAMDProvider(req.AuthProvider) {
		return okEnvelope(modelResponse{Provider: provider, Models: []modelInfo{}})
	}
	key, err := apiKey(req.StorageJSON)
	if err != nil {
		return nil, upstreamStatusError{status: 401, message: "AMD credential is invalid or missing api_key"}
	}

	resp, err := hostDo(req.HostCallbackID, "GET", baseURL+"/models", key, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamErrorFromResponse(resp.StatusCode, resp.Body)
	}
	var catalog amdModelList
	if err := json.Unmarshal(resp.Body, &catalog); err != nil {
		return nil, fmt.Errorf("decode AMD model catalog: %w", err)
	}
	models := make([]modelInfo, 0, len(catalog.Data))
	for _, model := range catalog.Data {
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(model.Name)
		if name == "" {
			name = id
		}
		models = append(models, modelInfo{
			ID:                         id,
			Object:                     "model",
			OwnedBy:                    provider,
			DisplayName:                name,
			SupportedGenerationMethods: []string{"chat"},
			ContextLength:              model.ContextLength,
			UserDefined:                true,
		})
	}
	return okEnvelope(modelResponse{Provider: provider, Models: models})
}

func execute(raw []byte) ([]byte, error) {
	var req executorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD executor request: %w", err)
	}
	key, err := apiKey(req.StorageJSON)
	if err != nil {
		return nil, upstreamStatusError{status: 401, message: "AMD credential is invalid or missing api_key"}
	}
	if len(req.Payload) == 0 {
		return nil, upstreamStatusError{status: 400, message: "AMD executor received an empty Chat Completions payload"}
	}
	resp, err := hostDo(req.HostCallbackID, "POST", baseURL+"/chat/completions", key, req.Payload)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamErrorFromResponse(resp.StatusCode, resp.Body)
	}
	return okEnvelope(executorResponse{Payload: resp.Body, Headers: resp.Headers})
}

func executeStream(raw []byte) ([]byte, error) {
	var req executorRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD stream executor request: %w", err)
	}
	key, err := apiKey(req.StorageJSON)
	if err != nil {
		return nil, upstreamStatusError{status: 401, message: "AMD credential is invalid or missing api_key"}
	}
	if len(req.Payload) == 0 {
		return nil, upstreamStatusError{status: 400, message: "AMD executor received an empty Chat Completions payload"}
	}

	result, err := hostCall("host.http.do_stream", hostHTTPRequest{
		HostCallbackID: req.HostCallbackID,
		Method:         "POST",
		URL:            baseURL + "/chat/completions",
		Headers:        amdHeaders(key, true),
		Body:           req.Payload,
	})
	if err != nil {
		return nil, err
	}
	var upstream hostStreamStartResponse
	if err := json.Unmarshal(result, &upstream); err != nil {
		return nil, fmt.Errorf("decode AMD upstream stream response: %w", err)
	}
	if upstream.StatusCode < 200 || upstream.StatusCode >= 300 || upstream.StreamID == "" {
		if upstream.StreamID != "" {
			_ = closeHostStream(upstream.StreamID)
		}
		return nil, upstreamStatusError{status: upstream.StatusCode, message: fmt.Sprintf("AMD upstream returned HTTP %d", upstream.StatusCode)}
	}

	// CLIProxyAPI owns the result stream ID. Returning without Chunks switches the
	// ABI bridge to asynchronous mode; this goroutine emits byte-exact SSE chunks.
	go forwardStream(req.StreamID, req.HostCallbackID, upstream.StreamID)
	return okEnvelope(executorStreamResponse{Headers: upstream.Headers})
}

func fetchQuota(raw []byte) ([]byte, error) {
	var req quotaFetchRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode AMD quota request: %w", err)
	}
	if !isAMDProvider(req.Provider) {
		return nil, upstreamStatusError{status: 400, message: "quota request is not for the AMD provider"}
	}
	key, err := apiKey(req.StorageJSON)
	if err != nil {
		return nil, upstreamStatusError{status: 401, message: "AMD credential is invalid or missing api_key"}
	}
	resp, err := hostDo(req.HostCallbackID, "GET", usageURL, key, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, upstreamErrorFromResponse(resp.StatusCode, resp.Body)
	}
	var usage amdUsageProfile
	if err := json.Unmarshal(resp.Body, &usage); err != nil {
		return nil, fmt.Errorf("decode AMD usage profile: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(usage.Status), "ok") {
		// Radeon explicitly documents not_available as unknown (rather than zero).
		return nil, fmt.Errorf("AMD usage service status is %q", usage.Status)
	}

	remainingFraction := 0.0
	if usage.DailyCostLimitUSD > 0 {
		remainingFraction = clamp(usage.DailyCostRemainingUSD / usage.DailyCostLimitUSD)
	}
	response := quotaFetchResponse{
		Summary: []quotaMetric{
			{Key: "daily_remaining_usd", Label: "Daily remaining", Value: usage.DailyCostRemainingUSD, Format: "currency", Currency: "USD"},
			{Key: "daily_used_usd", Label: "Daily used", Value: usage.DailyCostUsedUSD, Format: "currency", Currency: "USD"},
			{Key: "daily_limit_usd", Label: "Daily limit", Value: usage.DailyCostLimitUSD, Format: "currency", Currency: "USD"},
			{Key: "rpm_limit", Label: "Request limit", Value: usage.RPMLimit, Unit: "requests/min", Format: "number"},
			{Key: "today_requests", Label: "Today's requests", Value: usage.Today.Requests, Unit: "requests", Format: "number"},
			{Key: "today_tokens", Label: "Today's tokens", Value: usage.Today.TotalTokens, Unit: "tokens", Format: "number"},
			{Key: "today_errors", Label: "Today's errors", Value: usage.Today.Errors, Unit: "requests", Format: "number"},
		},
		Groups: []quotaGroup{{
			DisplayName: "Daily spend",
			Buckets: []quotaBucket{{
				Window:            "daily",
				RemainingFraction: remainingFraction,
				ResetTime:         usage.DailyResetAt,
				Description:       fmt.Sprintf("$%.4f remaining of $%.4f", usage.DailyCostRemainingUSD, usage.DailyCostLimitUSD),
			}},
		}},
	}
	return okEnvelope(response)
}

func forwardStream(pluginStreamID, callbackID, upstreamStreamID string) {
	_ = callbackID // The callback ID was used to bind host.http.do_stream to this request.
	defer func() { _ = closeHostStream(upstreamStreamID) }()
	if pluginStreamID == "" {
		return
	}
	for {
		result, err := hostCall("host.http.stream_read", streamReadRequest{StreamID: upstreamStreamID})
		if err != nil {
			_ = emitAndClose(pluginStreamID, "AMD stream read failed")
			return
		}
		var read hostStreamReadResponse
		if err := json.Unmarshal(result, &read); err != nil {
			_ = emitAndClose(pluginStreamID, "AMD stream response was invalid")
			return
		}
		if read.Error != "" {
			_ = emitAndClose(pluginStreamID, "AMD upstream stream failed")
			return
		}
		if len(read.Payload) > 0 {
			if _, err := hostCall("host.stream.emit", streamEmitRequest{StreamID: pluginStreamID, Payload: read.Payload}); err != nil {
				return
			}
		}
		if read.Done {
			_, _ = hostCall("host.stream.close", streamCloseRequest{StreamID: pluginStreamID})
			return
		}
	}
}

func emitAndClose(streamID, message string) error {
	_, _ = hostCall("host.stream.emit", streamEmitRequest{StreamID: streamID, Error: message})
	_, err := hostCall("host.stream.close", streamCloseRequest{StreamID: streamID, Error: message})
	return err
}

func closeHostStream(streamID string) error {
	_, err := hostCall("host.http.stream_close", streamReadRequest{StreamID: streamID})
	return err
}

func hostDo(callbackID, method, url, key string, body []byte) (hostHTTPResponse, error) {
	result, err := hostCall("host.http.do", hostHTTPRequest{
		HostCallbackID: callbackID,
		Method:         method,
		URL:            url,
		Headers:        amdHeaders(key, len(body) > 0),
		Body:           body,
	})
	if err != nil {
		return hostHTTPResponse{}, err
	}
	var response hostHTTPResponse
	if err := json.Unmarshal(result, &response); err != nil {
		return hostHTTPResponse{}, fmt.Errorf("decode AMD upstream response: %w", err)
	}
	return response, nil
}

func amdHeaders(key string, hasBody bool) map[string][]string {
	headers := map[string][]string{
		"Authorization": []string{"Bearer " + key},
		"Accept":        []string{"application/json"},
	}
	if hasBody {
		headers["Content-Type"] = []string{"application/json"}
	}
	return headers
}

func hostCall(method string, payload any) (json.RawMessage, error) {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	request := C.CBytes(rawPayload)
	defer C.free(request)

	var response C.cliproxy_buffer
	if C.call_host_api(cMethod, (*C.uint8_t)(request), C.size_t(len(rawPayload)), &response) != 0 {
		return nil, errors.New("CLIProxyAPI host callback failed")
	}
	if response.ptr == nil || response.len == 0 {
		return nil, errors.New("CLIProxyAPI host callback returned an empty response")
	}
	rawResponse := C.GoBytes(response.ptr, C.int(response.len))
	C.free_host_buffer(response.ptr, response.len)

	var reply envelope
	if err := json.Unmarshal(rawResponse, &reply); err != nil {
		return nil, fmt.Errorf("decode CLIProxyAPI host callback: %w", err)
	}
	if !reply.OK {
		if reply.Error != nil && reply.Error.Message != "" {
			return nil, errors.New(reply.Error.Message)
		}
		return nil, errors.New("CLIProxyAPI host callback failed")
	}
	return reply.Result, nil
}

func decodeCredential(raw []byte) (credential, bool, error) {
	var cred credential
	if len(raw) == 0 {
		return cred, false, errors.New("empty credential")
	}
	if err := json.Unmarshal(raw, &cred); err != nil {
		return cred, false, err
	}
	explicit := isAMDProvider(cred.Provider) || isAMDProvider(cred.Type)
	if !explicit {
		return cred, false, errors.New("not an AMD credential")
	}
	if strings.TrimSpace(cred.APIKey) == "" {
		return cred, true, errors.New("missing api_key")
	}
	return cred, true, nil
}

func apiKey(storage []byte) (string, error) {
	cred, explicit, err := decodeCredential(storage)
	if err != nil || !explicit {
		return "", errors.New("invalid AMD credential")
	}
	return strings.TrimSpace(cred.APIKey), nil
}

func makeAuthData(cred credential, storage []byte, filename, fallbackID string) authData {
	id := strings.TrimSpace(cred.ID)
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))
	}
	if id == "" {
		id = fallbackID
	}
	if id == "" {
		id = provider
	}
	label := strings.TrimSpace(cred.Label)
	if label == "" {
		label = "AMD Radeon Cloud"
	}
	if filename == "" {
		filename = id + ".json"
	}
	return authData{
		Provider:    provider,
		ID:          id,
		FileName:    filename,
		Label:       label,
		StorageJSON: storage,
		Metadata:    map[string]any{"type": provider},
	}
}

func isAMDProvider(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), provider)
}

func upstreamErrorFromResponse(status int, body []byte) error {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &payload) == nil && strings.TrimSpace(payload.Error.Message) != "" {
		return upstreamStatusError{status: status, message: "AMD upstream: " + payload.Error.Message}
	}
	return upstreamStatusError{status: status, message: fmt.Sprintf("AMD upstream returned HTTP %d", status)}
}

func clamp(value float64) float64 {
	return math.Max(0, math.Min(1, value))
}

func okEnvelope(value any) ([]byte, error) {
	result, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(envelope{OK: true, Result: result})
}

func errorEnvelope(code, message string, httpStatus int) []byte {
	raw, _ := json.Marshal(envelope{OK: false, Error: &rpcError{Code: code, Message: message, HTTPStatus: httpStatus}})
	return raw
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	ptr := C.CBytes(raw)
	if ptr == nil {
		return
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
}
