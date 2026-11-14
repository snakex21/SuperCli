// Copyright 2025 The Upstream Authors.
// SPDX-License-Identifier: Apache-2.0

package retry

// Jitter spreads n by +-pct percent of n.
func Jitter(n, pct int) (int, int) { return n - n*pct/100, n + n*pct/100 }
