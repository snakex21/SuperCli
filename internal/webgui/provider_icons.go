package webgui

import (
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"sync"

	"supercli/internal/system/config"
)

// Built-in provider logos live in assets/icons/providers/<key>.svg (from
// lobehub/lobe-icons, MIT; see THIRD_PARTY_NOTICES.md). A configured
// provider's logo is chosen from its type and address, never from the name
// the user gave it: "ja" pointing at api.anthropic.com is Anthropic.

// providerIcon is one built-in logo. Mono logos draw in currentColor and are
// shown as a mask in the theme's text colour; colour logos are shown as is.
type providerIcon struct {
	Key  string `json:"key"`
	Mono bool   `json:"mono"`
}

var (
	providerIconsOnce sync.Once
	providerIconList  []providerIcon
	providerIconKeys  map[string]bool
)

func builtinProviderIcons() []providerIcon {
	providerIconsOnce.Do(func() {
		providerIconKeys = map[string]bool{}
		entries, err := fs.ReadDir(assetsFS, "assets/icons/providers")
		if err != nil {
			return
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".svg") {
				continue
			}
			data, err := fs.ReadFile(assetsFS, "assets/icons/providers/"+name)
			if err != nil {
				continue
			}
			key := strings.TrimSuffix(name, ".svg")
			providerIconList = append(providerIconList, providerIcon{Key: key, Mono: strings.Contains(string(data), "currentColor")})
			providerIconKeys[key] = true
		}
		sort.Slice(providerIconList, func(i, j int) bool { return providerIconList[i].Key < providerIconList[j].Key })
	})
	return providerIconList
}

func isBuiltinProviderIcon(key string) bool {
	builtinProviderIcons()
	return providerIconKeys[key]
}

// providerHostIcons maps an address fragment (host suffix, or host:port for
// local servers) to a built-in logo. Longer, more specific entries first.
var providerHostIcons = []struct{ match, icon string }{
	{"chatgpt.com/backend-api/codex", "codex"},
	{"openai.azure.com", "azure"},
	{"api.openai.com", "openai"},
	{"anthropic.com", "anthropic"},
	{"generativelanguage.googleapis.com", "google"},
	{"aiplatform.googleapis.com", "vertexai"},
	{"groq.com", "groq"},
	{"together.xyz", "together"},
	{"together.ai", "together"},
	{"deepseek.com", "deepseek"},
	{"mistral.ai", "mistral"},
	{"openrouter.ai", "openrouter"},
	{"x.ai", "xai"},
	{"huggingface.co", "huggingface"},
	{"cline.bot", "cline"},
	{"nousresearch.com", "nous"},
	{"kilo.ai", "kilo"},
	{"kilocode.ai", "kilo"},
	{"opencode.ai", "opencode"},
	{"perplexity.ai", "perplexity"},
	{"fireworks.ai", "fireworks"},
	{"cerebras.ai", "cerebras"},
	{"cohere.com", "cohere"},
	{"cohere.ai", "cohere"},
	{"nvidia.com", "nvidia"},
	{"novita.ai", "novita"},
	{"chutes.ai", "chutes"},
	{"venice.ai", "venice"},
	{"lepton.ai", "lepton"},
	{"bigmodel.cn", "zhipu"},
	{"z.ai", "zhipu"},
	{"moonshot.ai", "moonshot"},
	{"moonshot.cn", "moonshot"},
	{"aliyuncs.com", "dashscope"},
	{"volces.com", "doubao"},
	{"volcengine.com", "doubao"},
	{"stepfun.com", "stepfun"},
	{"01.ai", "yi"},
	{"lingyiwanwu.com", "yi"},
	{"xiaomimimo.com", "xiaomi"},
	{"minimax.io", "minimax"},
	{"minimaxi.com", "minimax"},
	{"deepinfra.com", "deepinfra"},
	{"siliconflow.cn", "siliconflow"},
	{"siliconflow.com", "siliconflow"},
	{"baichuan-ai.com", "baichuan"},
	{"tencent.com", "tencent"},
	{"xf-yun.com", "iflytek"},
	{"baidubce.com", "qianfan"},
	{"sensenova.cn", "sensenova"},
	{"featherless.ai", "featherless"},
	{"amazonaws.com", "bedrock"},
	{"sambanova.ai", "sambanova"},
	{"hyperbolic.xyz", "hyperbolic"},
}

// providerPortIcons are local servers recognised by their default port.
var providerPortIcons = map[string]string{"1234": "lmstudio", "11434": "ollama"}

// ProviderIconKey picks the built-in logo for a provider from its protocol
// type and base URL; "" when nothing matches (the GUI then shows a letter).
func ProviderIconKey(typ, baseURL string) string {
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case config.ProviderClaude:
		return "claude"
	case config.ProviderCodex:
		return "codex"
	}
	raw := strings.TrimSpace(baseURL)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		host := strings.ToLower(u.Hostname())
		full := host + strings.ToLower(u.EscapedPath())
		for _, rule := range providerHostIcons {
			if strings.Contains(rule.match, "/") {
				if strings.HasPrefix(full, rule.match) {
					return rule.icon
				}
				continue
			}
			if host == rule.match || strings.HasSuffix(host, "."+rule.match) {
				return rule.icon
			}
		}
		if isLocalHost(host) {
			if icon := providerPortIcons[u.Port()]; icon != "" {
				return icon
			}
		}
	}
	switch strings.ToLower(strings.TrimSpace(typ)) {
	case config.ProviderAnthropic:
		return "anthropic"
	case config.ProviderOpencode:
		return "opencode"
	}
	return ""
}

func isLocalHost(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}
