// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package feed renders a read's released entries as Atom, RSS 2.0 and JSON Feed,
// passing each item through decorators selected by entry kind. Decorators add
// what is specific to a kind of serial, so the rest of the package never needs to
// know what an entry is. OPML export of feed URLs is here too.
package feed
