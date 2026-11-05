// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package discovery builds and maintains the ordered index of a source's
// entries. It holds the Strategy interface, the crawl loop and the fetcher
// that keeps andante polite, and it is the only part of andante that talks to
// other people's websites.
package discovery
