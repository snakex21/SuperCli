// Package mediagen implements explicitly configured, user-confirmed media
// generation. Merely constructing/discovering a tool performs no I/O.
package mediagen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"supercli/internal/system/config"
	"supercli/internal/tools/core"
)

const (
	MaxOutputBytes   int64 = 32 << 20
	maxPromptBytes         = 16000
	maxArgsBytes           = 24000
	maxMetadataBytes int64 = 1 << 20
)

// Tool uses only its configured provider/model. Confirm must obtain an actual
// interactive choice; a nil callback fails closed before a paid request.
// The config is snapshotted on construction so later mutation cannot redirect
// an in-flight request or silently change its approved parameters.
type Tool struct {
	baseDir string
	kind    string
	cfg     *config.MediaGenerationProviderConf
	confirm func(context.Context, string) error
	// Test-only dependency seams. No public insecure transport/config switch.
	client          *http.Client
	allowTestHTTP   bool
	timeoutOverride time.Duration
	pollOverride    time.Duration
	lookupEnv       func(string) string
	providerKey     func(string) (string, string, bool)
}

// WithProviderKeys lets api_key_provider reuse a configured chat provider key.
func (t *Tool) WithProviderKeys(lookup func(string) (string, string, bool)) *Tool {
	t.providerKey = lookup
	return t
}

// AdvertisedTools names the generation tools that are explicitly enabled in
// cfg. Callers mark them always-on so the model learns they exist: hidden,
// search-only tools were practically never called, because tool_search only
// matched near-literal wording such as "generate image". Disabled tools stay
// hidden and add nothing to the default prompt.
func AdvertisedTools(cfg config.MediaGenerationConf) []string {
	var names []string
	if cfg.Image != nil && cfg.Image.Enabled {
		names = append(names, "generate_image")
	}
	if cfg.Video != nil && cfg.Video.Enabled {
		names = append(names, "generate_video")
	}
	return names
}

func NewImage(baseDir string, cfg *config.MediaGenerationProviderConf, confirm func(context.Context, string) error) *Tool {
	return newTool(baseDir, "image", cfg, confirm)
}
func NewVideo(baseDir string, cfg *config.MediaGenerationProviderConf, confirm func(context.Context, string) error) *Tool {
	return newTool(baseDir, "video", cfg, confirm)
}
func newTool(baseDir, kind string, cfg *config.MediaGenerationProviderConf, confirm func(context.Context, string) error) *Tool {
	t := &Tool{baseDir: baseDir, kind: kind, confirm: confirm, lookupEnv: os.Getenv}
	if cfg != nil {
		clone := *cfg
		clone.AllowedParameters = append([]string(nil), cfg.AllowedParameters...)
		clone.AllowedDownloadHosts = append([]string(nil), cfg.AllowedDownloadHosts...)
		clone.DefaultParameters = make(map[string]any, len(cfg.DefaultParameters))
		// Supported parameters are scalars, validated before execution.
		for k, v := range cfg.DefaultParameters {
			clone.DefaultParameters[k] = v
		}
		t.cfg = &clone
	}
	return t
}

func (t *Tool) Spec() core.Tool {
	props := map[string]any{}
	if t.cfg != nil {
		for _, name := range t.cfg.AllowedParameters {
			if typ := parameterType(t.kind, name); typ != "" {
				props[name] = map[string]any{"type": typ}
			}
		}
	}
	fields := map[string]any{
		"prompt":     map[string]any{"type": "string", "description": "Describe the single output to generate."},
		"parameters": map[string]any{"type": "object", "additionalProperties": false, "properties": props, "description": "Optional explicitly configured generation parameters. Animated GIF (gpt-image-2.6 models): output_format \"gif\", frame_count 2-64, frame_duration_ms; keep frames small (e.g. 512x512)."},
	}
	if t.kind == "image" {
		def := ""
		if t.cfg != nil {
			def = t.cfg.Model
		}
		fields["model"] = map[string]any{"type": "string", "description": "Optional model id; only when the user names one. Default: " + def + "."}
	}
	schema, _ := json.Marshal(map[string]any{
		"type": "object", "additionalProperties": false, "required": []string{"prompt"},
		"properties": fields,
	})
	return core.Tool{Name: "generate_" + t.kind, Description: "Generate (create, draw, render) one new " + t.kind + " from a text prompt (" + t.kind + " generation, text-to-" + t.kind + ") using an explicitly configured provider after interactive confirmation. Saves a local file and returns preview metadata without attaching bytes to the model. No automatic paid fallback.", Schema: string(schema), Fn: t.Execute}
}

type arguments struct {
	Prompt     string         `json:"prompt"`
	Model      string         `json:"model"`
	Parameters map[string]any `json:"parameters"`
}

type runtimeConfig struct {
	base     *url.URL
	endpoint string
	model    string
	token    string
	local    bool // explicitly configured loopback/private server
	maxBytes int64
	timeout  time.Duration
	poll     time.Duration
	hosts    map[string]bool
}

func (t *Tool) Execute(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	result, err := t.execute(ctx, raw)
	if err != nil {
		return core.Result{Err: fmt.Errorf("generate_%s: %w", t.kind, err)}, nil
	}
	return result, nil
}
func (t *Tool) execute(ctx context.Context, raw json.RawMessage) (core.Result, error) {
	if err := ctx.Err(); err != nil {
		return core.Result{}, err
	}
	rc, err := t.validateConfig()
	if err != nil {
		return core.Result{}, err
	}
	if len(raw) > maxArgsBytes {
		return core.Result{}, errors.New("arguments exceed 24 KB limit")
	}
	var args arguments
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	dec.UseNumber()
	if err = dec.Decode(&args); err != nil {
		return core.Result{}, errors.New("invalid arguments; supply prompt and optional configured parameters")
	}
	if dec.Decode(new(any)) != io.EOF {
		return core.Result{}, errors.New("arguments must be one JSON object")
	}
	if strings.TrimSpace(args.Prompt) == "" || len(args.Prompt) > maxPromptBytes {
		return core.Result{}, errors.New("prompt must contain 1–16000 bytes")
	}
	if m := strings.TrimSpace(args.Model); m != "" {
		if t.kind != "image" {
			return core.Result{}, errors.New("model override is supported only for images")
		}
		if err = validModel(m); err != nil {
			return core.Result{}, err
		}
		rc.model = m
	}
	params, err := t.parameters(args.Parameters)
	if err != nil {
		return core.Result{}, err
	}
	if t.confirm == nil {
		return core.Result{}, errors.New("interactive generation confirmation is unavailable; no provider request was sent")
	}
	if rc.token == "" && !rc.local {
		if t.cfg.APIKeyProvider != "" {
			return core.Result{}, fmt.Errorf("provider %q has no API key", t.cfg.APIKeyProvider)
		}
		return core.Result{}, fmt.Errorf("configured API key environment variable %s is empty", t.cfg.APIKeyEnv)
	}
	encoded, _ := json.Marshal(params)
	question := fmt.Sprintf("Generate one %s with %s model %s at %s? This sends the prompt and parameters below to that provider and may incur its generation charges. No automatic retry or paid fallback.\n\nPrompt: %s\nParameters: %s", t.kind, t.cfg.Provider, rc.model, rc.base.String(), args.Prompt, encoded)
	if err = t.confirm(ctx, question); err != nil {
		return core.Result{}, fmt.Errorf("not approved: %w", err)
	}
	if err = ctx.Err(); err != nil {
		return core.Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, rc.timeout)
	defer cancel()
	client := t.client
	if client == nil && rc.local {
		client = newLocalHTTPClient()
		defer client.CloseIdleConnections()
	} else if client == nil {
		client = newHTTPClient()
		defer client.CloseIdleConnections()
	}
	// Always reject redirects, including when testing with an injected client.
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return errors.New("media generation redirects are not allowed")
	}
	params["prompt"] = args.Prompt
	if t.kind == "image" {
		params["model"] = rc.model
		params["n"] = 1
		return t.generateImage(ctx, &copyClient, rc, params)
	}
	return t.generateVideo(ctx, &copyClient, rc, params)
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var modelPath = regexp.MustCompile(`^[A-Za-z0-9_./-]+$`)

func (t *Tool) validateConfig() (runtimeConfig, error) {
	var rc runtimeConfig
	if t.cfg == nil || !t.cfg.Enabled {
		return rc, fmt.Errorf("not enabled; configure trusted [media_generation.%s] with enabled, provider, base_url, model and api_key_env", t.kind)
	}
	c := t.cfg
	expected := "openai"
	if t.kind == "video" {
		expected = "fal"
	}
	if c.Provider != expected {
		return rc, fmt.Errorf("%s generation requires provider %q", t.kind, expected)
	}
	u, local, err := t.baseURL(c.BaseURL)
	if err != nil || u.RawQuery != "" {
		return rc, errors.New("base_url must be an explicit HTTPS API root (or a local http server) without credentials, query or fragment")
	}
	if err = validModel(c.Model); err != nil {
		return rc, err
	}
	rc.local = local && t.kind == "image"
	if local && !rc.local {
		return rc, errors.New("local media servers are supported only for images")
	}
	rc.base = u
	rc.model = c.Model
	if name := strings.TrimSpace(c.APIKeyProvider); name != "" {
		if t.providerKey == nil {
			return rc, errors.New("api_key_provider is unavailable here")
		}
		base, key, ok := t.providerKey(name)
		if !ok {
			return rc, fmt.Errorf("api_key_provider %q is not a configured global provider", name)
		}
		if strings.TrimRight(strings.TrimSpace(base), "/") != strings.TrimRight(u.String(), "/") {
			return rc, fmt.Errorf("api_key_provider %q base_url must equal media base_url %s", name, u.String())
		}
		rc.token = strings.TrimSpace(key)
	} else if rc.local && c.APIKeyEnv == "" {
		// Local servers commonly need no key.
	} else {
		if !envName.MatchString(c.APIKeyEnv) {
			return rc, errors.New("api_key_env or api_key_provider is required")
		}
		rc.token = strings.TrimSpace(t.lookupEnv(c.APIKeyEnv))
	}
	if strings.ContainsAny(rc.token, "\r\n\x00") {
		return rc, errors.New("configured API key contains invalid characters")
	}
	rc.maxBytes = c.MaxBytes
	if rc.maxBytes == 0 {
		rc.maxBytes = MaxOutputBytes
	}
	if rc.maxBytes < 1 || rc.maxBytes > MaxOutputBytes {
		return rc, errors.New("max_bytes must be between 1 and 33554432 (32 MiB)")
	}
	seconds := c.TimeoutSeconds
	if seconds == 0 {
		seconds = 600
	}
	if seconds < 1 || seconds > 3600 {
		return rc, errors.New("timeout_seconds must be between 1 and 3600")
	}
	rc.timeout = time.Duration(seconds) * time.Second
	poll := c.PollIntervalMilliseconds
	if poll == 0 {
		poll = 2000
	}
	if poll < 250 || poll > 30000 {
		return rc, errors.New("poll_interval_milliseconds must be between 250 and 30000")
	}
	rc.poll = time.Duration(poll) * time.Millisecond
	if t.timeoutOverride > 0 {
		rc.timeout = t.timeoutOverride
	}
	if t.pollOverride > 0 {
		rc.poll = t.pollOverride
	}
	rc.hosts = make(map[string]bool, len(c.AllowedDownloadHosts))
	for _, h := range c.AllowedDownloadHosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" || strings.ContainsAny(h, "/:@*?#%\\") || strings.HasSuffix(h, ".") {
			return rc, errors.New("allowed_download_hosts must contain exact DNS hostnames, without wildcards or URL syntax")
		}
		if err := validateHostname(h); err != nil {
			return rc, err
		}
		rc.hosts[h] = true
	}
	if t.kind == "video" && len(rc.hosts) == 0 {
		return rc, errors.New("video requires explicitly allowed_download_hosts for unauthenticated media downloads")
	}
	endpoint := *u
	if t.kind == "image" {
		endpoint.Path = strings.TrimRight(u.Path, "/") + "/images/generations"
	} else {
		endpoint.Path = strings.TrimRight(u.Path, "/") + "/" + c.Model
	}
	endpoint.RawPath = ""
	rc.endpoint = endpoint.String()
	return rc, nil
}

func validModel(m string) error {
	if strings.TrimSpace(m) == "" || len(m) > 200 || !modelPath.MatchString(m) {
		return errors.New("model must be a plain model identifier")
	}
	for _, part := range strings.Split(m, "/") {
		if part == "" || part == "." || part == ".." {
			return errors.New("invalid model path")
		}
	}
	return nil
}

// baseURL accepts public HTTPS roots, or an explicitly configured local server
// (localhost, loopback or private IP literal) over http or https.
func (t *Tool) baseURL(raw string) (*url.URL, bool, error) {
	if u, err := url.Parse(raw); err == nil && u.Host != "" && u.User == nil && u.Fragment == "" && u.Opaque == "" &&
		(u.Scheme == "http" || u.Scheme == "https") && localHost(u.Hostname()) && !t.allowTestHTTP {
		return u, true, nil
	}
	u, err := strictURL(raw, t.allowTestHTTP)
	return u, false, err
}

func localHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Zone() == "" && (ip.IsLoopback() || ip.IsPrivate())
}

func parameterType(kind, name string) string {
	if kind == "image" {
		switch name {
		case "size", "quality", "background", "output_format":
			return "string"
		case "output_compression", "frame_count", "frame_duration_ms":
			return "integer"
		}
	}
	if kind == "video" {
		switch name {
		case "duration", "aspect_ratio", "resolution", "negative_prompt":
			return "string"
		case "seed", "num_frames", "num_inference_steps":
			return "integer"
		case "generate_audio":
			return "boolean"
		}
	}
	return ""
}
func (t *Tool) parameters(input map[string]any) (map[string]any, error) {
	allowed := map[string]bool{}
	for _, k := range t.cfg.AllowedParameters {
		if parameterType(t.kind, k) == "" {
			return nil, fmt.Errorf("unsupported configured parameter %q", k)
		}
		allowed[k] = true
	}
	merged := map[string]any{}
	for k, v := range t.cfg.DefaultParameters {
		merged[k] = v
	}
	for k, v := range input {
		merged[k] = v
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := merged[k]
		if !allowed[k] {
			return nil, fmt.Errorf("parameter %q is not configured in allowed_parameters", k)
		}
		switch parameterType(t.kind, k) {
		case "string":
			s, ok := v.(string)
			if !ok || len(s) > 2000 || strings.TrimSpace(s) == "" {
				return nil, fmt.Errorf("parameter %s requires a nonempty string up to 2000 bytes", k)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return nil, fmt.Errorf("parameter %s requires a boolean", k)
			}
		case "integer":
			data, _ := json.Marshal(v)
			n, err := json.Number(data).Int64()
			if err != nil || n < 0 || n > 2147483647 {
				return nil, fmt.Errorf("parameter %s requires a nonnegative 32-bit integer", k)
			}
			if k == "output_compression" && n > 100 {
				return nil, errors.New("output_compression must be 0–100")
			}
		}
	}
	return merged, nil
}
