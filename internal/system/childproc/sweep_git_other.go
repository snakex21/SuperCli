//go:build !windows

package childproc

func sweepOrphanedGit(func(string) bool) int { return 0 }
