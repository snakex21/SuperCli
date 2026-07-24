// Learned context limits moved to internal/llm so the web GUI can
// share them; this file keeps the app-local names alive.
package app

import "supercli/internal/llm"

type learnedLimits = llm.LearnedLimits

func loadLearnedLimits(dataDir string) *learnedLimits {
	return llm.LoadLearnedLimits(dataDir)
}
