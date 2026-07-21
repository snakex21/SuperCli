package main

import (
	"strings"
	"testing"

	"supercli/internal/webgui"
)

func TestValidateUIContract(t *testing.T) {
	for _, supported := range []int{0, webgui.UIContractVersion} {
		if err := validateUIContract(supported); err != nil {
			t.Fatalf("contract %d: %v", supported, err)
		}
	}
	for _, unsupported := range []int{-1, webgui.UIContractVersion + 1} {
		err := validateUIContract(unsupported)
		if err == nil || !strings.Contains(err.Error(), "engine provides") {
			t.Fatalf("contract %d error = %v", unsupported, err)
		}
	}
}
