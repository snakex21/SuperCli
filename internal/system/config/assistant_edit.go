package config

// AssistantEditEnabled reports whether AI replies may be edited in the GUI.
// Unset means off.
func AssistantEditEnabled(c TomlConfig) bool {
	return c.AssistantEdit != nil && *c.AssistantEdit
}

// AssistantEditModelSeesEnabled reports whether an edited reply replaces the
// original in the model context. Unset means off (display-only edits).
func AssistantEditModelSeesEnabled(c TomlConfig) bool {
	return c.AssistantEditModelSees != nil && *c.AssistantEditModelSees
}
