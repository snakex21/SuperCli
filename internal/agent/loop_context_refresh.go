package agent

import "context"

// SetContextWindowRefresh replaces the metadata refresh performed before the
// next user run. Call between runs, alongside a provider/model switch. Worker
// loops do not inherit this hook: their endpoints are refreshed by the owning
// user operation, rather than by every delegated tool invocation.
func (l *Loop) SetContextWindowRefresh(refresh func(context.Context) error) {
	l.refreshContextWindow = refresh
}
