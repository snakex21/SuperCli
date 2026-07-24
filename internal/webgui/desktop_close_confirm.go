package webgui

func shouldConfirmClose(_ string, hasActiveWork func() bool) bool {
	if hasActiveWork == nil || !hasActiveWork() {
		return false
	}
	return true
}
