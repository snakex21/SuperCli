package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type validationProbe struct {
	registry *Registry
	name     string
	calls    int
	lastArgs json.RawMessage
}

func newValidationProbe(t *testing.T, schema string) *validationProbe {
	t.Helper()
	return newNamedValidationProbe(t, "probe", schema)
}

// newNamedValidationProbe registers the schema under a chosen tool name, which
// matters when the assertion is about the error message: unknown-argument
// errors name the tool alongside its valid arguments.
func newNamedValidationProbe(t *testing.T, name, schema string) *validationProbe {
	t.Helper()
	p := &validationProbe{registry: NewRegistry(), name: name}
	p.registry.MustRegister(Tool{
		Name:        name,
		Description: "validation probe",
		Schema:      schema,
		Fn: func(_ context.Context, args json.RawMessage) (Result, error) {
			p.calls++
			p.lastArgs = append(p.lastArgs[:0], args...)
			return Result{Text: "called"}, nil
		},
	})
	return p
}

func (p *validationProbe) execute(t *testing.T, args string) Result {
	t.Helper()
	result, err := p.registry.Execute(context.Background(), p.name, json.RawMessage(args))
	if err != nil {
		t.Fatalf("Execute returned Go-level error: %v", err)
	}
	return result
}

func (p *validationProbe) requireValid(t *testing.T, args string) Result {
	t.Helper()
	before := p.calls
	result := p.execute(t, args)
	if result.Err != nil {
		t.Fatalf("arguments %s unexpectedly rejected: %v", args, result.Err)
	}
	if p.calls != before+1 {
		t.Fatalf("tool calls = %d, want %d", p.calls, before+1)
	}
	return result
}

func (p *validationProbe) requireInvalid(t *testing.T, args, contains string) Result {
	t.Helper()
	before := p.calls
	result := p.execute(t, args)
	if !errors.Is(result.Err, ErrInvalidToolArgs) {
		t.Fatalf("arguments %s error = %v, want ErrInvalidToolArgs", args, result.Err)
	}
	if contains != "" && !strings.Contains(result.Err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", result.Err, contains)
	}
	if p.calls != before {
		t.Fatalf("invalid arguments invoked tool: calls = %d, want %d", p.calls, before)
	}
	verdict := (Classifier{}).Classify(p.name, json.RawMessage(args), result)
	if verdict.Category != CategoryModel || verdict.Confidence < 0.9 {
		t.Fatalf("validation attribution = %+v, want high-confidence model error", verdict)
	}
	return result
}

func TestRegistryRejectsMalformedSupportedSchemasAtRegister(t *testing.T) {
	tests := []struct {
		name   string
		schema string
	}{
		{"malformed JSON", `{`},
		{"non-schema root", `[]`},
		{"invalid type", `{"type":"widget"}`},
		{"properties not object", `{"type":"object","properties":[]}`},
		{"required not strings", `{"type":"object","required":[1]}`},
		{"invalid additional properties", `{"type":"object","additionalProperties":7}`},
		{"negative size", `{"type":"array","minItems":-1}`},
		{"invalid regexp", `{"type":"string","pattern":"["}`},
		{"empty combinator", `{"type":"object","anyOf":[]}`},
		{"unbounded schema exponent", `{"type":"object","properties":{"n":{"type":"number","minimum":1e10001}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := NewRegistry()
			tool := okTool("bad")
			tool.Schema = test.schema
			if err := registry.Register(tool); err == nil {
				t.Fatal("Register unexpectedly accepted invalid schema")
			}
			if registry.Len() != 0 {
				t.Fatalf("registry mutated after failed Register: len = %d", registry.Len())
			}
		})
	}
}

func TestRegistrySchemaCompatibilityAndCompiledCoercion(t *testing.T) {
	t.Run("full schema", func(t *testing.T) {
		p := newValidationProbe(t, `{
			"type":"object",
			"properties":{"limit":{"type":"integer"}},
			"required":["limit"],
			"additionalProperties":false
		}`)
		p.requireValid(t, `{"limit":"5"}`)
		var got struct {
			Limit int `json:"limit"`
		}
		if err := json.Unmarshal(p.lastArgs, &got); err != nil || got.Limit != 5 {
			t.Fatalf("tool received %s (%+v, %v), want coerced integer 5", p.lastArgs, got, err)
		}
	})

	t.Run("historical shorthand", func(t *testing.T) {
		p := newValidationProbe(t, `{
			"file":{"type":"string"},
			"line":{"type":"integer"}
		}`)
		p.requireValid(t, `{"file":"notes.txt","line":"3"}`)
		var got struct {
			File string `json:"file"`
			Line int    `json:"line"`
		}
		if err := json.Unmarshal(p.lastArgs, &got); err != nil || got.Line != 3 || got.File != "notes.txt" {
			t.Fatalf("tool received %s (%+v, %v), want shorthand coercion", p.lastArgs, got, err)
		}
	})

	t.Run("empty schema remains fail-open", func(t *testing.T) {
		p := newValidationProbe(t, "")
		p.requireValid(t, `not-json`)
	})

	t.Run("unsupported keywords remain fail-open", func(t *testing.T) {
		p := newValidationProbe(t, `{
			"type":"object",
			"properties":{"name":{"type":"string","format":"future-format"}},
			"x-future-validator":{"strict":true}
		}`)
		p.requireValid(t, `{"name":"Ada"}`)
	})

	t.Run("JSON integer notation is accepted in schema", func(t *testing.T) {
		p := newValidationProbe(t, `{"type":"object","properties":{"s":{"type":"string","minLength":1.0}}}`)
		p.requireValid(t, `{"s":"x"}`)
	})
}

func TestRegistryValidatesObjectsAndAdditionalProperties(t *testing.T) {
	p := newValidationProbe(t, `{
		"type":"object",
		"properties":{
			"name":{"type":"string"},
			"nullable":{"type":["string","null"]}
		},
		"required":["name","nullable"],
		"additionalProperties":false
	}`)
	p.requireValid(t, `{"name":"Ada","nullable":null}`)
	p.requireInvalid(t, `{"nullable":null}`, "$.name: is required")
	p.requireInvalid(t, `{"name":9,"nullable":null}`, "$.name: expected string")
	p.requireInvalid(t, `{"name":"Ada","nullable":false}`, "$.nullable: expected null or string")
	p.requireInvalid(t, `{"name":"Ada","nullable":null,"extra":1}`, "$.extra: unknown argument")
	p.requireInvalid(t, `{`, "invalid JSON")

	t.Run("additionalProperties schema", func(t *testing.T) {
		additional := newValidationProbe(t, `{
			"type":"object",
			"properties":{"known":{"type":"string"}},
			"additionalProperties":{"type":"integer"}
		}`)
		additional.requireValid(t, `{"known":"yes","count":2}`)
		additional.requireInvalid(t, `{"known":"yes","count":"two"}`, "$.count: expected integer")
	})

	t.Run("unsupported patternProperties does not cause false rejection", func(t *testing.T) {
		future := newValidationProbe(t, `{
			"type":"object",
			"patternProperties":{"^x-":{"type":"string"}},
			"additionalProperties":false
		}`)
		future.requireValid(t, `{"x-future":7}`)
	})
}

func TestRegistryValidatesEnumConstAndPrimitiveConstraints(t *testing.T) {
	p := newValidationProbe(t, `{
		"type":"object",
		"properties":{
			"mode":{"type":["string","number","null"],"enum":["fast",2,null]},
			"fixed":{"const":{"a":1,"b":[true]}},
			"code":{"type":"string","minLength":2,"maxLength":4,"pattern":"^[A-Z]+$"},
			"score":{"type":"number","minimum":-1.5,"maximum":10}
		},
		"required":["mode","fixed","code","score"]
	}`)
	valid := `{"mode":2.0,"fixed":{"b":[true],"a":1.0},"code":"AB","score":2.5}`
	p.requireValid(t, valid)
	p.requireInvalid(t, `{"mode":"slow","fixed":{"a":1,"b":[true]},"code":"AB","score":2}`, "$.mode: value is not in enum")
	p.requireInvalid(t, `{"mode":null,"fixed":{"a":2,"b":[true]},"code":"AB","score":2}`, "$.fixed: value does not match const")
	p.requireInvalid(t, `{"mode":null,"fixed":{"a":1,"b":[true]},"code":"A","score":2}`, "$.code: length must be at least 2")
	p.requireInvalid(t, `{"mode":null,"fixed":{"a":1,"b":[true]},"code":"ABCDE","score":2}`, "$.code: length must be at most 4")
	p.requireInvalid(t, `{"mode":null,"fixed":{"a":1,"b":[true]},"code":"Ab","score":2}`, "$.code: does not match required pattern")
	p.requireInvalid(t, `{"mode":null,"fixed":{"a":1,"b":[true]},"code":"AB","score":-1.6}`, "$.score: must be at least")
	p.requireInvalid(t, `{"mode":null,"fixed":{"a":1,"b":[true]},"code":"AB","score":11}`, "$.score: must be at most")
}

func TestRegistryValidatesArrays(t *testing.T) {
	p := newValidationProbe(t, `{
		"type":"object",
		"properties":{"tags":{
			"type":"array","items":{"type":"string"},
			"minItems":2,"maxItems":3,"uniqueItems":true
		}},
		"required":["tags"]
	}`)
	p.requireValid(t, `{"tags":["a","b"]}`)
	p.requireInvalid(t, `{"tags":["a"]}`, "needs at least 2 items")
	p.requireInvalid(t, `{"tags":["a","b","c","d"]}`, "allows at most 3 items")
	p.requireInvalid(t, `{"tags":["a","a"]}`, "duplicates item")
	p.requireInvalid(t, `{"tags":["a",2]}`, "$.tags[1]: expected string")

	t.Run("numeric uniqueness is semantic", func(t *testing.T) {
		numbers := newValidationProbe(t, `{"type":"object","properties":{"v":{"type":"array","uniqueItems":true}}}`)
		numbers.requireInvalid(t, `{"v":[1,1.0]}`, "duplicates item")
	})

	t.Run("tuple items", func(t *testing.T) {
		tuple := newValidationProbe(t, `{
			"type":"object",
			"properties":{"v":{"type":"array","items":[{"type":"string"},{"type":"integer"}],"additionalItems":false}}
		}`)
		tuple.requireValid(t, `{"v":["x",2]}`)
		tuple.requireInvalid(t, `{"v":["x",2,true]}`, "additional item is not allowed")
	})
}

func TestRegistryValidatesCombinators(t *testing.T) {
	p := newValidationProbe(t, `{
		"type":"object",
		"properties":{
			"any":{"anyOf":[{"type":"string","minLength":2},{"type":"integer","minimum":5}]},
			"one":{"oneOf":[{"type":"number"},{"type":"integer"}]},
			"all":{"allOf":[{"type":"string"},{"minLength":2},{"pattern":"^[A-Z]+$"}]}
		},
		"required":["any","one","all"]
	}`)
	p.requireValid(t, `{"any":5,"one":1.5,"all":"AB"}`)
	p.requireInvalid(t, `{"any":false,"one":1.5,"all":"AB"}`, "must match at least one anyOf branch")
	p.requireInvalid(t, `{"any":"ok","one":1,"all":"AB"}`, "must match exactly one oneOf branch")
	p.requireInvalid(t, `{"any":"ok","one":1.5,"all":"a"}`, "length must be at least 2")
}

// TestRegistryRejectsUnknownToolArgumentsInsteadOfDroppingThem pins the fix for
// a production failure: the model called search_code with "file" instead of
// "path", validation passed, json.Unmarshal dropped the key, and the tool
// searched the whole repository and reported success. Rejecting the call is
// only half the fix - the message has to name the arguments the tool does have,
// or the model's cheapest next move is to send the same call again.
func TestRegistryRejectsUnknownToolArgumentsInsteadOfDroppingThem(t *testing.T) {
	// Schema copied from internal/tools/search/search_code.go.
	search := newNamedValidationProbe(t, "search_code", `{
		"type": "object",
		"properties": {
			"query": {"type": "string"},
			"path":  {"type": "string"},
			"max":   {"type": "integer"}
		},
		"required": ["query"]
	}`)
	search.requireValid(t, `{"query":"ProviderConf","path":"internal/llm/providers","max":20}`)

	result := search.requireInvalid(t,
		`{"query":"ProviderConf","max":20,"file":"internal/llm/providers"}`,
		"$.file: unknown argument")
	message := result.Err.Error()
	for _, want := range []string{"search_code", "max", "path", "query"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not mention %q: the model cannot repair the call from it", message, want)
		}
	}
	// "file" for "path" is a semantic mix-up, not a typo. Guessing there would
	// be worse than the list itself.
	if strings.Contains(message, "did you mean") {
		t.Fatalf("error %q guesses a replacement for a non-typo", message)
	}

	// A misspelled required argument must be reported as the misspelling, not as
	// a missing argument: "$.query: is required" would leave "quer" in the next
	// call and cost another turn.
	t.Run("obvious typo gets a hint", func(t *testing.T) {
		typo := search.requireInvalid(t, `{"quer":"x"}`, "$.quer: unknown argument")
		if !strings.Contains(typo.Err.Error(), `did you mean "query"?`) {
			t.Fatalf("error %q does not suggest the near-identical name", typo.Err)
		}
	})

	t.Run("invoke_tool keeps its explicit additionalProperties", func(t *testing.T) {
		// Schema copied from internal/agent/tool_invoke.go. The dispatcher passes
		// target arguments as arg.<name> keys; sealing this root would break every
		// dispatched call.
		invoke := newNamedValidationProbe(t, "invoke_tool", `{"type":"object","properties":{"tool":{"type":"string"},"args":{"type":"object","description":"Target arguments for native tool calling"}},"required":["tool"],"additionalProperties":true}`)
		invoke.requireValid(t, `{"tool":"read_lines","arg.file":"sample.txt","arg.from":"1"}`)
		invoke.requireValid(t, `{"tool":"read_lines","args":{"file":"sample.txt","from":1,"to":2}}`)
	})

	t.Run("shorthand schema is sealed after normalization", func(t *testing.T) {
		// Schema copied from internal/tools/files/edit_line.go: root is the bare
		// property map, so the fix has to survive normalizeToolSchemaRoot.
		edit := newNamedValidationProbe(t, "edit_line", `{"file":{"type":"string"},
"line":{"type":"integer"},
"new_content":{"type":"string"},
"expected_old":{"type":"string"}}`)
		edit.requireValid(t, `{"file":"a.go","line":3,"new_content":"x","expected_old":"y"}`)
		typo := edit.requireInvalid(t, `{"fil":"a.go","line":3,"new_content":"x"}`, "$.fil: unknown argument")
		for _, want := range []string{`did you mean "file"?`, "expected_old, file, line, new_content"} {
			if !strings.Contains(typo.Err.Error(), want) {
				t.Fatalf("error %q does not contain %q", typo.Err, want)
			}
		}
	})

	t.Run("nested argument envelopes stay fail-open", func(t *testing.T) {
		// Schema copied from internal/tools/mcp/bridge.go: arguments carries an
		// arbitrary remote server's arguments and must never be sealed.
		bridge := newNamedValidationProbe(t, "mcp_bridge", `{"type":"object","properties":{"action":{"type":"string","enum":["list","search","call"]},"server":{"type":"string"},"query":{"type":"string"},"tool":{"type":"string"},"arguments":{"type":"object","additionalProperties":true}},"required":["action"]}`)
		bridge.requireValid(t, `{"action":"call","server":"web","tool":"fetch","arguments":{"url":"https://example.com","depth":2,"opts":{"raw":true}}}`)
		bridge.requireInvalid(t, `{"action":"list","argments":{"a":1}}`, "$.argments: unknown argument")
	})

	t.Run("schemas without declared arguments stay fail-open", func(t *testing.T) {
		for _, schema := range []string{`{}`, `{"type":"object"}`, `{"type":"object","properties":{}}`} {
			open := newValidationProbe(t, schema)
			open.requireValid(t, `{"anything":1}`)
		}
	})

	t.Run("combinator roots are sealed only when branches add nothing", func(t *testing.T) {
		// ask_user's shape: branches pick which declared properties are required.
		either := newNamedValidationProbe(t, "ask_user", `{
			"type":"object",
			"properties":{"question":{"type":"string"},"questions":{"type":"array"}},
			"anyOf":[{"required":["question"]},{"required":["questions"]}]
		}`)
		either.requireValid(t, `{"question":"go on?"}`)
		either.requireInvalid(t, `{"question":"go on?","prompt":"x"}`, "$.prompt: unknown argument")

		// A branch that declares its own properties makes the root list an
		// incomplete picture, so the root must not be sealed.
		branching := newValidationProbe(t, `{
			"type":"object",
			"properties":{"kind":{"type":"string"}},
			"anyOf":[{"properties":{"extra":{"type":"integer"}}},{"required":["kind"]}]
		}`)
		branching.requireValid(t, `{"kind":"a","extra":1}`)
	})
}

func TestNearestArgumentOnlySuggestsTypographicNeighbours(t *testing.T) {
	valid := []string{"expected_old", "file", "line", "new_content"}
	tests := []struct {
		name string
		want string
	}{
		{"fil", "file"},
		{"File", "file"},
		{"lines", "line"},
		{"new_contents", "new_content"},
		{"path", ""},         // semantic mix-up, not a typo
		{"content", ""},      // three edits away from new_content
		{"", ""},             // nothing to match
		{"expected", ""},     // four edits away
		{"xyzzy", ""},        // unrelated
		{"expected_new", ""}, // one edit, but a different argument is meant
	}
	for _, test := range tests {
		if got := nearestArgument(test.name, valid); got != test.want {
			t.Fatalf("nearestArgument(%q) = %q, want %q", test.name, got, test.want)
		}
	}
	t.Run("ties stay silent", func(t *testing.T) {
		if got := nearestArgument("fro", []string{"from", "frob", "to"}); got != "" {
			t.Fatalf("nearestArgument with two equally near names = %q, want no guess", got)
		}
	})
}

func TestRegistryRejectsExtremeArgumentExponentWithoutCallingTool(t *testing.T) {
	p := newValidationProbe(t, `{"type":"object","properties":{"value":{"type":"number"}},"required":["value"]}`)
	for _, args := range []string{`{"value":1e10001}`, `{"value":1e-10001}`, `{"value":1e999999999999999999999}`} {
		p.requireInvalid(t, args, "$.value: invalid number")
	}
}
