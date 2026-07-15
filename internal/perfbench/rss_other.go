//go:build !windows && !linux

package perfbench

func processRSSMB(int) float64 { return 0 }
