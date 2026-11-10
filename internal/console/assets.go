package console

import "embed"

// EmbeddedAssets holds the compiled SPA. dist/ contains a placeholder in the
// repo; `make web` (or the Docker build) fills it with the real bundle.
//
//go:embed all:dist
var EmbeddedAssets embed.FS
