package config

import _ "embed"

//go:embed example.toml
var ExampleTOML string

// MinimalTOML is what `ishakat config init` writes by default: just enough
// to be valid (schema, an empty [app]) plus comments pointing at `provider
// add` and `config init --full`. ExampleTOML is what --full writes instead —
// the two are deliberately separate embeds/constants, not one derived from
// the other, so that TestExampleTOMLInSync's byte-for-byte drift guard on
// ExampleTOML keeps meaning what it already means without also having to
// track this file.
//
//go:embed minimal.toml
var MinimalTOML string
