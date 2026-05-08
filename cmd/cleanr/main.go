package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"os"
	"regexp"
	"strings"
)

// extractID safely extracts the ID, regardless of flat or nested MongoDB format.
func extractID(record map[string]interface{}) string {
	// 1. Check for standard flat "_id" or "id" (string)
	if id, ok := record["_id"].(string); ok {
		return id
	}
	if id, ok := record["id"].(string); ok {
		return id
	}

	// 2. Check for nested MongoDB {"_id": {"$oid": "..."}}
	if idObj, ok := record["_id"].(map[string]interface{}); ok {
		if oid, ok := idObj["$oid"].(string); ok {
			return oid
		}
	}
	return ""
}

func main() {
	// Setup command-line flags
	inFile := flag.String("in", "data.json", "Input JSON file path")
	outFile := flag.String("out", "cleaned_data.json", "Output JSON file path")
	fieldsFlag := flag.String("fields", "name,slogan", "Comma-separated list of fields to check")
	autoAction := flag.String("auto", "", "Auto mode: 'clear' (removes text), 'delete' (removes record), or 'keep'. Leave empty for interactive.")
	flag.Parse()

	fieldsToCheck := strings.Split(*fieldsFlag, ",")
	autoMode := strings.ToLower(strings.TrimSpace(*autoAction))

	// Read input file
	data, err := os.ReadFile(*inFile)
	if err != nil {
		fmt.Printf("Error reading file %q: %v\n", *inFile, err)
		os.Exit(1)
	}

	var records []map[string]interface{}
	if err := json.Unmarshal(data, &records); err != nil {
		fmt.Printf("Error parsing JSON: %v\n", err)
		os.Exit(1)
	}

	// Compile Regexes once into a slice for cleaner iteration
	spamPatterns := []*regexp.Regexp{
		regexp.MustCompile(`[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}`),              // Emails
		regexp.MustCompile(`(?i)(https?://|www\.)[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}(/[^\s]*)?`), // URLs
		regexp.MustCompile(`(?i)<[a-z/][^>]*>`),                                           // HTML Tags
		regexp.MustCompile(`(?i)hs=[a-f0-9]{20,}`),                                        // Spam Hashes
		regexp.MustCompile(`(?i)\$\d+(?:,\d+)?\s+deposit`),                                // Money Spam
	}

	scanner := bufio.NewScanner(os.Stdin)
	idsToDelete := make(map[string]bool)

	// ==========================================
	// PASS 1: Identify and Flag
	// ==========================================
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

			// Collect all matches across all patterns
			for _, pattern := range spamPatterns {
				matches = append(matches, pattern.FindAllString(decodedStr, -1)...)
			}

			if len(matches) == 0 {
				continue
			}

			for _, match := range matches {
				// If running in auto-mode, skip prompts
				answer := autoMode
				if answer == "" {
					fmt.Printf("\n[!] Suspicious data found in '%s' (Field: %s)\n", recordName, field)
					fmt.Printf("    Data: %s\n", match)
					fmt.Print("    Action: (d)elete entire record, (c)lear text only, (k)eep text: ")
					scanner.Scan()
					answer = strings.ToLower(strings.TrimSpace(scanner.Text()))
				}

				if answer == "d" || answer == "delete" {
					if recordID != "" {
						idsToDelete[recordID] = true
						if autoMode == "" {
							fmt.Println("    -> Record flagged for complete deletion.")
						}
						shouldDeleteRecord = true
						break // Exit the match loop, record is already doomed
					} else if autoMode == "" {
						fmt.Println("    -> Error: Could not extract ID. Cannot delete record.")
					}
				} else if answer == "c" || answer == "clear" {
					decodedStr = strings.ReplaceAll(decodedStr, match, "")
					if autoMode == "" {
						fmt.Println("    -> Text cleared.")
					}
				} else if autoMode == "" {
					fmt.Println("    -> Kept.")
				}
			}

			// Save the cleared string back to the record if we aren't deleting the whole thing
			if !shouldDeleteRecord {
				record[field] = strings.TrimSpace(decodedStr)
				records[i] = record
			}
		}
	}

	// ==========================================
	// PASS 2: Filter and Build Final Slice
	// ==========================================
	// Pre-allocate slice capacity to optimize memory and prevent resizing overhead
	finalRecords := make([]map[string]interface{}, 0, len(records))

	for _, record := range records {
		id := extractID(record)
		if !idsToDelete[id] {
			finalRecords = append(finalRecords, record)
		}
	}

	fmt.Printf("\n--- Cleanup Summary ---\n")
	fmt.Printf("Original count: %d\n", len(records))
	fmt.Printf("Deleted count:  %d\n", len(idsToDelete))
	fmt.Printf("Final count:    %d\n", len(finalRecords))

	cleanedJSON, err := json.MarshalIndent(finalRecords, "", "  ")
	if err != nil {
		fmt.Printf("Error marshaling cleaned JSON: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile(*outFile, cleanedJSON, 0644); err != nil {
		fmt.Printf("Error writing to file: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("✅ Complete. Saved to %s\n", *outFile)
}
