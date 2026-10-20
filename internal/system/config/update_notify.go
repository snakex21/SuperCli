package config

// UpdateNotifyEnabled reports whether the background new-release notice is on.
// Unset means on: the check is one small request a day and never downloads.
func UpdateNotifyEnabled(c TomlConfig) bool {
	return c.UpdateNotify == nil || *c.UpdateNotify
}
