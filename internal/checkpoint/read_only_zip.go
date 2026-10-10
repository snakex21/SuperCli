package checkpoint

import "encoding/json"

// ZIP listing/text reads should never capture the workspace or archive bytes.
func checkpointToolReadOnly(name string, raw json.RawMessage) bool {
	if name != "read_zip" {
		return checkpointCommandReadOnly(name, raw)
	}
	var a struct {
		Action string `json:"action"`
	}
	if json.Unmarshal(raw, &a) != nil {
		return false
	}
	return a.Action == "" || a.Action == "list" || a.Action == "read"
}
