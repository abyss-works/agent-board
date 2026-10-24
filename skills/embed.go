// Package skills embeds the agent skills so the agb binary can install the
// copy that matches its own release.
package skills

import "embed"

// FS holds the tool-agb skill folder as it is in the repository.
//
//go:embed tool-agb
var FS embed.FS
