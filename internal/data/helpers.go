package data

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

// descriptionPromptLimit caps how much of a company's description gets
// included in the AI-answer prompt (see Engine.GenerateAnswer).
const descriptionPromptLimit = 150

var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// truncateForPrompt shortens a plain-text description to at most limit
// runes, breaking on a word boundary rather than mid-word.
func truncateForPrompt(description string, limit int) string {
	if utf8.RuneCountInString(description) <= limit {
		return description
	}
	runes := []rune(description)
	cutAt := limit
	for cutAt > 0 && runes[cutAt] != ' ' {
		cutAt--
	}
	if cutAt == 0 {
		cutAt = limit
	}
	return string(runes[:cutAt]) + "..."
}
