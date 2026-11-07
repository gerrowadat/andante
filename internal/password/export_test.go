// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

package password

// Hold takes a slot, and returns how to give it back.
func Hold(h *Hasher) func() {
	h.slots <- struct{}{}
	return h.release
}
