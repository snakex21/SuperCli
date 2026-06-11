//go:build !windows

package tools

import (
	"context"
	"fmt"
)

// outlookRun is the non-Windows stub: Outlook COM automation is
// only available on Windows desktops.
func outlookRun(_ context.Context, _ outlookArgs) (string, error) {
	return "", fmt.Errorf("outlook_mail is only available on Windows (requires desktop Outlook)")
}
