package core

import "encoding/json"

// Compile a complete, small list once. It is only appended to an enum failure;
// valid requests and provider tool definitions stay unchanged. Large/structured
// choices keep the short existing error rather than suggesting a partial value.
func compileEnumHint(values []any) string {
	if len(values) > 16 {
		return ""
	}
	for _, value := range values {
		switch typed := value.(type) {
		case nil, bool:
		case string:
			if len(typed) > 128 {
				return ""
			}
		case json.Number:
			if len(typed) > 128 {
				return ""
			}
		default:
			return ""
		}
	}
	raw, err := json.Marshal(values)
	if err != nil || len(raw) > 512 {
		return ""
	}
	return "; allowed values: " + string(raw)
}
