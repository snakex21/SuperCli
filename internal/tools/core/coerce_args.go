package core

import (
	"encoding/json"
	"strconv"
)

// coerce_args.go makes tool-argument parsing lenient for small and
// local models. Such models constantly stringify scalars — they send
// {"limit":"5"} instead of {"limit":5} or {"async":"true"} instead of
// {"async":true}. Standard json.Unmarshal into a typed struct then
// fails with "cannot unmarshal string into Go struct field ... of type
// int", and the agent loop burns a whole retry step (and re-sends the
// full context) for a purely cosmetic mismatch.
//
// CoerceArgs rewrites the raw argument JSON so stringified scalars
// match the types declared in the tool's JSON Schema, BEFORE the tool
// unmarshals them. It is applied once at the Registry.Execute
// chokepoint, so every tool benefits without per-tool changes.
//
// The transform is deliberately conservative:
//   - only top-level properties named in the schema are touched;
//   - only string values are candidates (a value already of the right
//     type is left alone);
//   - a string is converted only when it is a valid literal of the
//     target type ("5"->5, "true"->true). A non-numeric string for an
//     int field is left untouched so the tool still reports the real
//     error rather than us guessing.
//
// If anything is missing or unparseable (no schema, args not an
// object, schema not parseable) CoerceArgs returns the input
// unchanged — it must never make parsing worse than before.

// CoerceArgs returns args with top-level scalar fields coerced to the
// types declared in schema. On any uncertainty it returns args as-is.
func CoerceArgs(schema string, args json.RawMessage) json.RawMessage {
	if len(schema) == 0 || len(args) == 0 {
		return args
	}
	types := schemaPropTypes(schema)
	if len(types) == 0 {
		return args
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil {
		// Not a JSON object (array, scalar, malformed) — leave it for
		// the tool / existing repair path to handle.
		return args
	}

	changed := false
	for key, want := range types {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		// Only string values are coercion candidates.
		var s string
		if json.Unmarshal(raw, &s) != nil {
			continue
		}
		if c, did := coerceScalar(s, want); did {
			obj[key] = c
			changed = true
		}
	}
	if !changed {
		return args
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return args
	}
	return out
}

// coerceScalar converts the string s to a JSON literal of the wanted
// type. The second return reports whether a conversion happened; when
// false the caller keeps the original (still-string) value so the
// tool surfaces the genuine error.
func coerceScalar(s, want string) (json.RawMessage, bool) {
	switch want {
	case "integer":
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return json.RawMessage(strconv.FormatInt(n, 10)), true
		}
	case "number":
		if _, err := strconv.ParseFloat(s, 64); err == nil {
			// Re-emit the original literal (already validated) so we
			// don't reformat e.g. "1.50" into "1.5" unnecessarily.
			return json.RawMessage(s), true
		}
	case "boolean":
		if b, err := strconv.ParseBool(s); err == nil {
			return json.RawMessage(strconv.FormatBool(b)), true
		}
	}
	return nil, false
}

// schemaPropTypes parses a JSON-Schema object and returns a map of
// top-level property name -> declared type ("integer", "number",
// "boolean", "string", ...). Properties whose type is absent or is a
// list of types are skipped (ambiguous — leave them alone). Returns an
// empty map on any parse failure.
func schemaPropTypes(schema string) map[string]string {
	var s struct {
		Properties map[string]struct {
			Type json.RawMessage `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schema), &s); err != nil {
		return nil
	}
	out := make(map[string]string, len(s.Properties))
	for name, p := range s.Properties {
		var t string
		if json.Unmarshal(p.Type, &t) != nil {
			// type omitted or a union ([]string) — skip.
			continue
		}
		out[name] = t
	}
	return out
}
