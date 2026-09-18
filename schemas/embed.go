// Package schemas embeds the JSON schemas that Speccy validates configuration against.
package schemas

import _ "embed"

// Profile is the JSON schema for profiles (SDD §10.3).
//
//go:embed profile.schema.json
var Profile []byte
