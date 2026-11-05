// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package server runs andante's two listeners and wires them together: the
// public listener serves feeds, the admin listener serves the API, health and
// metrics. It also holds the shared middleware and the public feed routes.
package server
