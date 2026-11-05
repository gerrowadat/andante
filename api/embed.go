// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package api embeds the admin API's OpenAPI document, the contract the server
// validates requests against and serves to clients.
package api

import _ "embed"

// OpenAPI is the OpenAPI document, as YAML.
//
//go:embed openapi.yaml
var OpenAPI []byte
