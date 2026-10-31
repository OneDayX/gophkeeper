// Package openapi holds the Swagger (OpenAPI v2) description of the GophKeeper
// REST API. The JSON next to this file is generated from the proto files by
// protoc-gen-openapiv2 (see `make proto`); this package embeds it so the
// server can serve it.
package openapi

import _ "embed" // for the go:embed directive

// Spec is the Swagger description in JSON.
//
//go:embed gophkeeper.swagger.json
var Spec []byte
