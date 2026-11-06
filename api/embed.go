// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 Dave O'Connor

// Package api embeds the OpenAPI documents, the contracts the services validate
// requests against.
package api

import _ "embed"

// OpenAPI is the OpenAPI document, as YAML.
//
//go:embed openapi.yaml
var OpenAPI []byte

// Catalogue is the catalogue service's OpenAPI document, as YAML.
//
//go:embed catalogue.yaml
var Catalogue []byte
