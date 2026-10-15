//go:build !windows

package main

func claimSingleInstance(string) (func(), bool, error) {
	return func() {}, false, nil
}

func notifyAlreadyRunning(string, string) {}

func showEventName(string) string { return "" }

func showExistingInstance(string) bool { return false }
