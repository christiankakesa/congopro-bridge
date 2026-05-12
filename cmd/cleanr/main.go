package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"html"
	"os"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/logger"
)

// extractID safely extracts the ID, regardless of flat or nested MongoDB format.
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

func main() {
	logger.Init(true)
	inFile := flag.String("in", "data.json", "Input JSON file path")
	outFile := flag.String("out", "cleaned_data.json", "Output JSON file path")
	fieldsFlag := flag.String("fields", "name,slogan", "Comma-separated list of fields to check")
	autoAction := flag.String("auto", "", "Auto mode: 'clear' (removes text), 'delete' (removes record), or 'keep'. Leave empty for interactive.")
	flag.Parse()

	fieldsToCheck := strings.Split(*fieldsFlag, ",")
	autoMode := strings.ToLower(strings.TrimSpace(*autoAction))

	data, err := os.ReadFile(*inFile)
	if err != nil {
		log.Error().Msgf("Can't read file %q: %v", *inFile, err)
		os.Exit(1)
	}

	var records []map[string]interface{}
	if err := json.Unmarshal(data, &records); err != nil {
		log.Error().Msgf("Can't parse JSON: %v", err)
		os.Exit(1)
	}

	spamPatterns := []*regexp.Regexp{
		regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),              // Emails
		regexp.MustCompile(`(?i)(https?://|www\.)[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}(/[^\s]*)?`), // URLs
		regexp.MustCompile(`(?i)<[a-z/][^>]*>`),                                           // HTML Tags
		regexp.MustCompile(`(?i)hs=[a-f0-9]{20,}`),                                        // Spam Hashes
		regexp.MustCompile(`(?i)\$\d+(?:,\d+)?\s+deposit`),                                // Money Spam
	}

	scanner := bufio.NewScanner(os.Stdin)
	idsToDelete := make(map[string]bool)

	for i, record := range records {
		recordName := "Unknown"
		if n, ok := record["name"].(string); ok {
			recordName = n
		}

		recordID := extractID(record)
		shouldDeleteRecord := false

		for _, field := range fieldsToCheck {
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
					log.Warn().Msgf("\n[!] Suspicious data found in '%s' (Field: %s)", recordName, field)
					log.Info().Msgf("    Data: %s", match)
					log.Info().Msg("    Action: (d)elete entire record, (c)lear text only, (k)eep text: ")
					scanner.Scan()
					answer = strings.ToLower(strings.TrimSpace(scanner.Text()))
				}

				if answer == "d" || answer == "delete" {
					if recordID != "" {
						idsToDelete[recordID] = true
						if autoMode == "" {
							log.Info().Msg("    -> Record flagged for complete deletion.")
						}
						shouldDeleteRecord = true
						break // Exit the match loop, record is already doomed
					} else if autoMode == "" {
						log.Warn().Msg("    -> Could not extract ID. Cannot delete record.")
					}
				} else if answer == "c" || answer == "clear" {
					decodedStr = strings.ReplaceAll(decodedStr, match, "")
					if autoMode == "" {
						log.Info().Msg("    -> Text cleared.")
					}
				} else if autoMode == "" {
					log.Info().Msg("    -> Kept.")
				}
			}

			if !shouldDeleteRecord {
				record[field] = strings.TrimSpace(decodedStr)
				records[i] = record
			}
		}
	}

	finalRecords := make([]map[string]interface{}, 0, len(records))

	for _, record := range records {
		id := extractID(record)
		if !idsToDelete[id] {
			finalRecords = append(finalRecords, record)
		}
	}

	log.Info().Msgf("\n--- Cleanup Summary ---")
	log.Info().Msgf("Original count: %d", len(records))
	log.Info().Msgf("Deleted count:  %d", len(idsToDelete))
	log.Info().Msgf("Final count:    %d", len(finalRecords))

	cleanedJSON, err := json.MarshalIndent(finalRecords, "", "  ")
	if err != nil {
		log.Error().Msgf("Error marshaling cleaned JSON: %v", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outFile, cleanedJSON, 0644); err != nil {
		log.Error().Msgf("Error writing to file: %v", err)
		os.Exit(1)
	}

	log.Info().Msgf("✅ Complete. Saved to %s", *outFile)
}
