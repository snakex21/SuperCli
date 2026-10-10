package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// prepareOwnedStandardResponsesRequest preserves legacy decoding for typed
// requests whose tool Parameters may contain arbitrary JSON.
func prepareOwnedStandardResponsesRequest(req codexRequest, key string, reasoningModel bool, sampling Sampling) ([]byte, error) {
	return prepareStandardResponsesTypedRequest(req, key, reasoningModel, sampling, false)
}

// prepareAssembledStandardResponsesRequest accepts only an unmodified request
// freshly returned by assembleCodexRequestWithEffort. That assembler supplies
// private tool schema bytes already decoded and encoded by
// normalizeToolSchemaChecked, including legacy numeric and string semantics.
func prepareAssembledStandardResponsesRequest(req codexRequest, key string, reasoningModel bool, sampling Sampling) ([]byte, error) {
	return prepareStandardResponsesTypedRequest(req, key, reasoningModel, sampling, true)
}

func prepareStandardResponsesTypedRequest(req codexRequest, key string, reasoningModel bool, sampling Sampling, canonicalTools bool) ([]byte, error) {
	root, err := standardResponsesRequestMapWithCanonicalTools(&req, key, reasoningModel, sampling, canonicalTools)
	if err != nil {
		// Keep exact legacy replacement-character and opaque parser/error behavior.
		body, oldErr := json.Marshal(req)
		if oldErr != nil {
			return nil, oldErr
		}
		return prepareStandardResponsesRequest(body, key, reasoningModel, sampling)
	}
	return json.Marshal(root)
}

func standardResponsesRequestMap(req *codexRequest, key string, reasoningModel bool, sampling Sampling) (map[string]any, error) {
	return standardResponsesRequestMapWithCanonicalTools(req, key, reasoningModel, sampling, false)
}

func standardResponsesRequestMapWithCanonicalTools(req *codexRequest, key string, reasoningModel bool, sampling Sampling, canonicalTools bool) (map[string]any, error) {
	validStrings := func(ss ...string) bool {
		for _, s := range ss {
			if !utf8.ValidString(s) {
				return false
			}
		}
		return true
	}
	if !validStrings(req.Model, req.Instructions, req.ToolChoice) || !validStrings(req.Include...) {
		return nil, fmt.Errorf("legacy string encoding")
	}
	var input []any
	for _, item := range req.Input {
		if item.Raw != nil {
			var raw any
			if err := json.Unmarshal(item.Raw, &raw); err != nil {
				return nil, err
			}
			input = append(input, raw)
			continue
		}
		if !validStrings(item.Type, item.Role, item.Name, item.Arguments, item.CallID, item.Output) {
			return nil, fmt.Errorf("legacy string encoding")
		}
		m := map[string]any{"type": item.Type}
		if item.Role != "" {
			m["role"] = item.Role
		}
		if item.Name != "" {
			m["name"] = item.Name
		}
		if item.Arguments != "" {
			m["arguments"] = item.Arguments
		}
		if item.CallID != "" {
			m["call_id"] = item.CallID
		}
		if item.Output != "" {
			m["output"] = item.Output
		}
		if len(item.Content) != 0 {
			parts := make([]any, 0, len(item.Content))
			for _, part := range item.Content {
				if !validStrings(part.Type, part.Text, part.ImageURL) {
					return nil, fmt.Errorf("legacy string encoding")
				}
				p := map[string]any{"type": part.Type}
				if part.Text != "" {
					p["text"] = part.Text
				}
				if part.ImageURL != "" {
					p["image_url"] = part.ImageURL
				}
				parts = append(parts, p)
			}
			m["content"] = parts
		}
		input = append(input, m)
	}
	toolList := make([]any, 0, len(req.Tools))
	for _, tool := range req.Tools {
		if !validStrings(tool.Type, tool.Name, tool.Description) {
			return nil, fmt.Errorf("legacy string encoding")
		}
		m := map[string]any{"type": tool.Type, "name": tool.Name, "strict": tool.Strict}
		if tool.Description != "" {
			m["description"] = tool.Description
		}
		if len(tool.Parameters) != 0 {
			if canonicalTools {
				// Reuse only assembler-owned canonical bytes; final Marshal still
				// validates and applies the standard JSON escaping rules.
				m["parameters"] = tool.Parameters
			} else {
				var schema any
				if err := json.Unmarshal(tool.Parameters, &schema); err != nil {
					return nil, err
				}
				m["parameters"] = schema
			}
		}
		toolList = append(toolList, m)
	}
	root := map[string]any{
		"model": req.Model, "input": input, "tools": toolList,
		"tool_choice": req.ToolChoice, "parallel_tool_calls": req.ParallelToolCalls,
		"store": req.Store, "stream": req.Stream, "include": req.Include,
	}
	if req.Instructions != "" {
		root["instructions"] = req.Instructions
	}
	if req.Reasoning != nil {
		if !validStrings(req.Reasoning.Effort, req.Reasoning.Summary) {
			return nil, fmt.Errorf("legacy string encoding")
		}
		m := map[string]any{}
		if req.Reasoning.Effort != "" {
			m["effort"] = req.Reasoning.Effort
		}
		if req.Reasoning.Summary != "" {
			m["summary"] = req.Reasoning.Summary
		}
		root["reasoning"] = m
	}
	if !reasoningModel {
		if sampling.Temperature != nil {
			root["temperature"] = *sampling.Temperature
		}
		if sampling.TopP != nil {
			root["top_p"] = *sampling.TopP
		}
	} else {
		reasoning, _ := root["reasoning"].(map[string]any)
		if reasoning == nil {
			reasoning = make(map[string]any)
		}
		if reasoning["effort"] != "none" {
			reasoning["summary"] = "detailed"
		}
		root["reasoning"] = reasoning
		root["include"] = []string{"reasoning.encrypted_content"}
	}
	key = strings.TrimSpace(key)
	if key == "" {
		key = "supercli"
	}
	// key is inserted after the legacy full-body decode, so invalid UTF-8 in
	// this field deliberately retains normal final json.Marshal semantics.
	root["prompt_cache_key"] = key
	return root, nil
}
