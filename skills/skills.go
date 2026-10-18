// Package skills embeds the agent skills shipped with Artifax.
package skills

import _ "embed"

// Artifax is the SKILL.md that teaches agents to publish to Artifax.
//
//go:embed artifax/SKILL.md
var Artifax string

// ExampleURL is the placeholder instance URL used in the skill text.
const ExampleURL = "https://artifax.example.com"
