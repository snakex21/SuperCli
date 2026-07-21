//go:build !windows

package mail

import (
	"context"
	"fmt"
)

func outlookIndexMessages(context.Context, string, int) ([]OutlookIndexMessage, error) {
	return nil, fmt.Errorf("Outlook indexing is only available on Windows with desktop Outlook")
}
