//go:build windows

package webgui

import "testing"

func TestCreateTaskbarBadgeIcon(t *testing.T) {
	icon, err := createTaskbarBadgeIcon(3)
	if err != nil {
		t.Fatalf("create native badge icon: %v", err)
	}
	if icon == 0 {
		t.Fatal("native badge icon is null")
	}
	destroyIcon.Call(icon)
}
