// Package spec embeds the normative CODI schemas and SDK contract so the Go code
// validates against exactly what is documented.
package spec

import "embed"

// Schemas holds schemas/*.schema.json.
//
//go:embed schemas/*.schema.json
var Schemas embed.FS

// SDKTypes is the TypeScript contract of @codi/sdk.
//
//go:embed sdk/codi-sdk.d.ts
var SDKTypes string

// SchemaBaseURL is the $id prefix of every CODI schema.
const SchemaBaseURL = "https://codi.dev/schemas/v1alpha1/"
