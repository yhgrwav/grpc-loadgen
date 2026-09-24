// Copyright 2026 yhgrwav
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metrics

import (
	"fmt"
	"time"
)

// view is one copy of a distribution as the percentile rule reads it. Ranks
// are 1-based; measuredAt and combinedAt return the upper bound of the bucket
// holding that rank. combinedAt merges measured and censored and may be
// expensive: the rule asks for it only when the percentile is not exact.
type view interface {
	counts() (measured, censored int64)
	thresholds() (lo, hi int64)
	measuredAt(rank int64) int64
	combinedAt(rank int64) int64
}

// percentile is the one rule behind Snapshot.Percentile and Buffer.Percentile.
func percentile(v view, p float64) Quantile {
	if p < 0 || p > 1 {
		panic(fmt.Sprintf("metrics: Percentile(%v): p is a fraction in [0, 1], not a 0..100 scale", p))
	}

	measuredN, censoredN := v.counts()
	n := measuredN + censoredN
	if n == 0 {
		return Quantile{}
	}

	rank := rankFor(p, n)
	lo, hi := v.thresholds()

	// Exact when the rank-th measured value's bucket lies at or below every
	// censored threshold: a censored observation's true value exceeds its
	// threshold, so none of them can take that rank.
	if rank <= measuredN {
		if m := v.measuredAt(rank); m <= lo {
			return Quantile{Value: time.Duration(m), Exact: true, Defined: true}
		}
	}

	// A single threshold c is the bound when fewer than rank measured values
	// can be below it: the rank-th measured one lies in a bucket above c's.
	if lo == hi && (measuredN < rank || v.measuredAt(rank) > highestEquivalent(lo)) {
		return Quantile{Value: time.Duration(lo), Defined: true}
	}

	return Quantile{Value: time.Duration(lowestEquivalent(v.combinedAt(rank))), Defined: true}
}
