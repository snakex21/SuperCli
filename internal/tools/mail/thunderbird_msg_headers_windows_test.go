//go:build windows

package mail

import (
	"strings"
	"testing"
)

func TestSuperCLIRFC822HeadersSchema(t *testing.T) {
	if !strings.Contains(supercliRFC822HeadersSchema, "SuperCLI-RFC822-Headers") {
		t.Fatalf("unexpected custom RFC822 header schema: %s", supercliRFC822HeadersSchema)
	}
}

func TestMSGAttachmentTempNameIsShortAndKeepsExtension(t *testing.T) {
	long := strings.Repeat("%31%38%34%36", 80) + ".pdf"
	got := msgAttachmentTempName(long, 7)
	if len(got) > 32 {
		t.Fatalf("temporary attachment name is too long: %d %q", len(got), got)
	}
	if !strings.HasPrefix(got, "007-") || !strings.HasSuffix(got, ".pdf") {
		t.Fatalf("unexpected temporary attachment name: %q", got)
	}
}
