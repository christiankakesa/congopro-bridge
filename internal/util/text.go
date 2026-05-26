package util

import (
	"strings"
	"unicode"

	"github.com/rs/zerolog/log"
	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

func TextNormalize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, err := transform.String(t, s)
	if err != nil {
		log.Warn().Msgf("[ads] failed to normalize string %q: %v", s, err)
		return s
	}

	return result
}
