//go:build !windows

package mail

import (
	"context"
	"fmt"
)

type msgConversionMeta struct {
	Subject             string `json:"subject,omitempty"`
	AttachmentCount     int    `json:"attachmentCount"`
	UsedOriginalHeaders bool   `json:"usedOriginalHeaders"`
	PreservedHTML       bool   `json:"preservedHtml"`
	PreservedMessageID  bool   `json:"preservedMessageId"`
	Converter           string `json:"converter"`
}

func convertOutlookMSGToEML(ctx context.Context, source, destination string) (msgConversionMeta, error) {
	_ = ctx
	_ = source
	_ = destination
	return msgConversionMeta{Converter: "outlook_com"}, fmt.Errorf(".msg conversion currently requires Windows with classic desktop Outlook installed")
}
