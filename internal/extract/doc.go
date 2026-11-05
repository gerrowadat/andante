// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package extract pulls fields such as title, images, text, enclosures, date
// and next link out of a fetched page or feed item. Extractors are chosen by
// entry kind and prefer open metadata before falling back to heuristics.
package extract
