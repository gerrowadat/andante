// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package web

import "time"

// The event streams' timings, short enough for tests not to wait on them. They're
// set once, before any test starts a server.
func init() {
	heartbeat = 100 * time.Millisecond
	settle = 20 * time.Millisecond
}
