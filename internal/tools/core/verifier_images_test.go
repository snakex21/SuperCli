package core

import (
	"errors"
	"testing"
)

func TestVerifyReadChecksPrimaryAndAdditionalImages(t *testing.T) {
	valid := &ImageContent{Data: []byte("pixels")}
	empty := &ImageContent{MediaType: "image/png"}
	cases := []struct {
		name   string
		result Result
		ok     bool
	}{
		{"text", Result{Text: "read content"}, true},
		{"primary", Result{Image: valid}, true},
		{"empty_primary", Result{Image: empty}, false},
		{"additional", Result{Images: []*ImageContent{valid}}, true},
		{"nil_additional", Result{Images: []*ImageContent{nil}}, false},
		{"empty_additional", Result{Images: []*ImageContent{empty}}, false},
		{"both_valid", Result{Image: valid, Images: []*ImageContent{valid}}, true},
		{"empty_primary_valid_additional", Result{Image: empty, Images: []*ImageContent{valid}}, false},
		{"valid_primary_nil_additional", Result{Image: valid, Images: []*ImageContent{nil}}, false},
		{"valid_primary_empty_additional", Result{Image: valid, Images: []*ImageContent{empty}}, false},
		{"reported_error", Result{Image: empty, Images: []*ImageContent{nil}, Err: errors.New("tool failed")}, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := (DefaultVerifier{}).Verify(Check{Family: "read", Result: tt.result})
			if got.OK != tt.ok {
				t.Fatalf("OK=%v want %v, reason=%q", got.OK, tt.ok, got.Reason)
			}
			if !tt.ok && got.Reason != "verification failed: image is empty" {
				t.Fatalf("reason=%q", got.Reason)
			}
		})
	}
	if string(valid.Data) != "pixels" || len(empty.Data) != 0 {
		t.Fatal("verification mutated image bytes")
	}
}
