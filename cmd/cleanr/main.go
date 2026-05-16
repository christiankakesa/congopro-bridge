package main

import (
	"bufio"
	"encoding/json"
	"html"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"congopro-bridge/internal/logger"
)

// --- ID extraction (unchanged) ---

func extractID(record map[string]interface{}) string {
	if id, ok := record["_id"].(string); ok {
		return id
	}
	if id, ok := record["id"].(string); ok {
		return id
	}
	if idObj, ok := record["_id"].(map[string]interface{}); ok {
		if oid, ok := idObj["$oid"].(string); ok {
			return oid
		}
	}
	return ""
}

// --- Feature: spam detection ---

var spamPatterns = []*regexp.Regexp{
	regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),              // Emails
	regexp.MustCompile(`(?i)(https?://|www\.)[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}(/[^\s]*)?`), // URLs
	regexp.MustCompile(`(?i)<[a-z/][^>]*>`),                                           // HTML tags
	regexp.MustCompile(`(?i)hs=[a-f0-9]{20,}`),                                        // Spam hashes
	regexp.MustCompile(`(?i)\$\d+(?:,\d+)?\s+deposit`),                                // Money spam
}

func runSpamFeature(
	records []map[string]interface{},
	fields []string,
	autoMode string,
	scanner *bufio.Scanner,
) ([]map[string]interface{}, int) {
	idsToDelete := make(map[string]bool)

	for i, record := range records {
		recordName := "Unknown"
		if n, ok := record["name"].(string); ok {
			recordName = n
		}
		recordID := extractID(record)
		shouldDeleteRecord := false

		for _, field := range fields {
			val, ok := record[field].(string)
			if !ok || val == "" {
				continue
			}

			decodedStr := html.UnescapeString(val)
			var matches []string
			for _, pattern := range spamPatterns {
				matches = append(matches, pattern.FindAllString(decodedStr, -1)...)
			}
			if len(matches) == 0 {
				continue
			}

			for _, match := range matches {
				answer := autoMode
				if answer == "" {
					log.Warn().Msgf("\n[!] Suspicious data in %q (field: %s)", recordName, field)
					log.Info().Msgf("    Match: %s", match)
					log.Info().Msg("    Action: (d)elete record  (c)lear field  (k)eep: ")
					scanner.Scan()
					answer = strings.ToLower(strings.TrimSpace(scanner.Text()))
				}

				switch {
				case answer == "d" || answer == "delete":
					if recordID != "" {
						idsToDelete[recordID] = true
						shouldDeleteRecord = true
						if autoMode == "" {
							log.Info().Msg("    -> Record flagged for deletion.")
						}
					} else if autoMode == "" {
						log.Warn().Msg("    -> No ID found; cannot delete record.")
					}
				case answer == "c" || answer == "clear":
					decodedStr = strings.ReplaceAll(decodedStr, match, "")
					if autoMode == "" {
						log.Info().Msg("    -> Field cleared.")
					}
				default:
					if autoMode == "" {
						log.Info().Msg("    -> Kept.")
					}
				}

				if shouldDeleteRecord {
					break
				}
			}

			if !shouldDeleteRecord {
				record[field] = strings.TrimSpace(decodedStr)
				records[i] = record
			}
		}
	}

	final := make([]map[string]interface{}, 0, len(records))
	for _, record := range records {
		if !idsToDelete[extractID(record)] {
			final = append(final, record)
		}
	}
	return final, len(idsToDelete)
}

// --- Feature: capitalization ---

// titleCase applies context-aware title casing. Rules applied in order:
//
//  1. First word all-lowercase → capitalize it ("hello" → "Hello").
//  2. First word all-uppercase with ≤2 letters → recase ("DA" → "Da").
//     Short all-caps tokens are unlikely to be intentional brand names.
//  3. First word anything else (mixed-case or long all-caps) → preserve
//     ("iPhone", "CHOLA", "McGee" kept verbatim).
//  4. Subsequent words: recase to Title Case unless already correct OR the
//     first word was mixed-case (signals intentional casing on the whole
//     string, e.g. "iPhone accessories" — "accessories" is kept lowercase).
//
// Examples:
//
//	"DA SAFI DECOR"      → "Da Safi Decor"
//	"iPhone accessories" → "iPhone accessories"
//	"McGee & Sons"       → "McGee & Sons"
//	"hello world"        → "Hello World"
//	"Groupe CHOLA"       → "Groupe Chola"
//	"CHOLA Groupe"       → "CHOLA Groupe"
//	"CHOLA GROUPE"       → "CHOLA Groupe"
//	"CHOLA GrOupe"       → "CHOLA Groupe"
func titleCase(s string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}

	firstAllLower := wordAllLower(words[0])
	firstAllUpper := wordAllUpper(words[0])
	firstMixed := !firstAllLower && !firstAllUpper

	for i, w := range words {
		if i == 0 {
			switch {
			case firstAllLower:
				words[i] = recaseWord(w)
			case firstAllUpper && letterCount(w) <= 2:
				// Short all-caps first word ("DA"): not a brand, recase.
				words[i] = recaseWord(w)
			default:
				// Mixed-case or long all-caps first word: preserve verbatim.
			}
			continue
		}

		// Subsequent words.
		if firstMixed {
			// Mixed-case first word: only fix wrongly-cased words; leave
			// all-lowercase words alone (they are intentionally lowercase).
			if !isTitleCased(w) && !wordAllLower(w) {
				words[i] = recaseWord(w)
			}
		} else {
			// Uniform first word (all-lower or all-upper): normalize every
			// subsequent word that is not already title-cased.
			if !isTitleCased(w) {
				words[i] = recaseWord(w)
			}
		}
	}
	return strings.Join(words, " ")
}

// recaseWord returns w with its first letter uppercased and the rest lowercased.
func recaseWord(w string) string {
	runes := []rune(w)
	if len(runes) == 0 {
		return w
	}
	runes[0] = unicode.ToUpper(runes[0])
	for j := 1; j < len(runes); j++ {
		runes[j] = unicode.ToLower(runes[j])
	}
	return string(runes)
}

// isTitleCased returns true if w's first letter is uppercase and all
// subsequent letters are lowercase (non-letter runes are ignored).
func isTitleCased(w string) bool {
	letters := make([]rune, 0, len(w))
	for _, r := range w {
		if unicode.IsLetter(r) {
			letters = append(letters, r)
		}
	}
	if len(letters) == 0 {
		return true
	}
	if !unicode.IsUpper(letters[0]) {
		return false
	}
	for _, r := range letters[1:] {
		if unicode.IsUpper(r) {
			return false
		}
	}
	return true
}

// wordAllUpper returns true if w has at least one letter and every letter is uppercase.
func wordAllUpper(w string) bool {
	hasLetter := false
	for _, r := range w {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsLower(r) {
				return false
			}
		}
	}
	return hasLetter
}

// wordAllLower returns true if w has at least one letter and every letter is lowercase.
func wordAllLower(w string) bool {
	hasLetter := false
	for _, r := range w {
		if unicode.IsLetter(r) {
			hasLetter = true
			if unicode.IsUpper(r) {
				return false
			}
		}
	}
	return hasLetter
}

// letterCount returns the number of Unicode letters in w.
func letterCount(w string) int {
	n := 0
	for _, r := range w {
		if unicode.IsLetter(r) {
			n++
		}
	}
	return n
}

func runCapFeature(records []map[string]interface{}, fields []string) []map[string]interface{} {
	for _, record := range records {
		for _, field := range fields {
			val, ok := record[field].(string)
			if !ok || val == "" {
				log.Debug().Msgf("[cap] field %q missing or not a string, skipping", field)
				continue
			}
			capped := titleCase(val)
			log.Debug().Msgf("[cap] %q: %q -> %q", field, val, capped)
			record[field] = capped
		}
	}
	return records
}

// --- Main ---

func main() {
	logger.Init(true)

	app := &cli.App{
		Name:  "data-cleaner",
		Usage: "Clean and normalize JSON record files",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "in",
				Aliases: []string{"i"},
				Value:   "data.json",
				Usage:   "Input JSON file path",
				EnvVars: []string{},
			},
			&cli.StringFlag{
				Name:    "out",
				Aliases: []string{"o"},
				Value:   "cleaned_data.json",
				Usage:   "Output JSON file path",
			},
			// Shared fields default — overridden per-feature when set explicitly.
			&cli.StringFlag{
				Name:  "fields",
				Value: "name,slogan",
				Usage: "Default comma-separated fields applied to all enabled features",
			},

			// --- Spam feature ---
			&cli.BoolFlag{
				Name:  "feature-spam",
				Usage: "Enable spam detection and removal",
			},
			&cli.StringFlag{
				Name:  "spam-fields",
				Usage: "Fields to scan for spam (overrides --fields for this feature)",
			},
			&cli.StringFlag{
				Name:  "spam-auto",
				Usage: "Auto action for spam: 'clear', 'delete', or 'keep' (leave empty for interactive)",
			},

			// --- Capitalize feature ---
			&cli.BoolFlag{
				Name:  "feature-cap",
				Usage: "Enable title-case capitalization of fields",
			},
			&cli.StringFlag{
				Name:  "cap-fields",
				Usage: "Fields to capitalize (overrides --fields for this feature)",
			},
		},

		Action: func(c *cli.Context) error {
			inFile := c.String("in")
			outFile := c.String("out")
			defaultFields := splitFields(c.String("fields"))

			featureSpam := c.Bool("feature-spam")
			featureCap := c.Bool("feature-cap")

			if !featureSpam && !featureCap {
				log.Warn().Msg("No features enabled. Use --feature-spam and/or --feature-cap.")
				return nil
			}

			// Load records
			data, err := os.ReadFile(inFile)
			if err != nil {
				log.Error().Msgf("Can't read %q: %v", inFile, err)
				os.Exit(1)
			}
			var records []map[string]interface{}
			if err := json.Unmarshal(data, &records); err != nil {
				log.Error().Msgf("Can't parse JSON: %v", err)
				os.Exit(1)
			}
			originalCount := len(records)

			deletedCount := 0
			scanner := bufio.NewScanner(os.Stdin)

			// Run spam feature
			if featureSpam {
				fields := resolveFields(c.String("spam-fields"), defaultFields)
				autoMode := strings.ToLower(strings.TrimSpace(c.String("spam-auto")))
				records, deletedCount = runSpamFeature(records, fields, autoMode, scanner)
				log.Info().Msgf("[spam] Deleted %d record(s).", deletedCount)
			}

			// Run capitalize feature
			if featureCap {
				fields := resolveFields(c.String("cap-fields"), defaultFields)
				records = runCapFeature(records, fields)
				log.Info().Msgf("[cap] Capitalized fields: %s", strings.Join(fields, ", "))
			}

			// Summary
			log.Info().Msg("--- Cleanup Summary ---")
			log.Info().Msgf("Original : %d", originalCount)
			log.Info().Msgf("Deleted  : %d", deletedCount)
			log.Info().Msgf("Final    : %d", len(records))

			// Write output
			cleanedJSON, err := json.MarshalIndent(records, "", "  ")
			if err != nil {
				log.Error().Msgf("Error marshaling JSON: %v", err)
				os.Exit(1)
			}
			if err := os.WriteFile(outFile, cleanedJSON, 0644); err != nil {
				log.Error().Msgf("Error writing %q: %v", outFile, err)
				os.Exit(1)
			}
			log.Info().Msgf("✅ Saved to %s", outFile)
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Send()
	}
}

// resolveFields returns the feature-specific fields if set, otherwise the default.
func resolveFields(featureFields string, defaultFields []string) []string {
	if featureFields != "" {
		return splitFields(featureFields)
	}
	return defaultFields
}

func splitFields(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
