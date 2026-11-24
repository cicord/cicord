// Package spec embeds the normative Cicord schemas and SDK contract so the Go code
// validates against exactly what is documented.
package spec

import "embed"

// Schemas holds schemas/*.schema.json.
//
//go:embed schemas/*.schema.json
var Schemas embed.FS

// SDKTypes is the TypeScript contract of @cicord/sdk.
//
//go:embed sdk/cicord-sdk.d.ts
var SDKTypes string

// SchemaBaseURL is the $id prefix of every Cicord schema.
const SchemaBaseURL = "https://cicord.dev/schemas/v1alpha1/"
