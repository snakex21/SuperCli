package main

import "testing"

func TestNestCafeProfileEnablesFullFilesystemAccess(t *testing.T) {
	if !resolveAllowAll("nestcafe", false, false, "") {
		t.Fatal("NestCafe profile did not enable office filesystem access")
	}
	if resolveAllowAll("supercli", false, false, "") {
		t.Fatal("SuperCli default unexpectedly enabled full filesystem access")
	}
	if !resolveAllowAll("supercli", false, false, "true") {
		t.Fatal("explicit environment opt-in was ignored")
	}
}
