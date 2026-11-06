// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package strategies holds the discovery strategies andante ships.
package strategies

import "github.com/gerrowadat/andante/internal/discovery"

// All returns every strategy.
func All() []discovery.Strategy {
	return []discovery.Strategy{Crawl{}, Feed{}, FeedPaged{}, Numeric{}}
}
