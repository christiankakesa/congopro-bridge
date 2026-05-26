package main

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	"gopkg.in/yaml.v3"

	"congopro-bridge/internal/logger"
)

// =============================================================================
// 1. CORE INTERFACES & CONFIGURATION TYPES
// =============================================================================

type PipelineStep interface {
	Name() string
	Process(ctx context.Context, records []map[string]interface{}) ([]map[string]interface{}, error)
}

// PipelineConfig represents the top-level YAML structure.
type PipelineConfig struct {
	Pipeline []StepConfig `yaml:"pipeline"`
}

// StepConfig represents a single step configuration from the YAML file.
type StepConfig struct {
	Type    string   `yaml:"type"`
	Fields  []string `yaml:"fields"`
	Workers int      `yaml:"workers,omitempty"`
	Action  string   `yaml:"action,omitempty"`
}

// =============================================================================
// 2. CONFIGURATION LOADER & PIPELINE FACTORY
// =============================================================================

func LoadConfig(filename string) ([]PipelineStep, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config PipelineConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return buildPipeline(config)
}

func buildPipeline(config PipelineConfig) ([]PipelineStep, error) {
	var pipeline []PipelineStep

	for _, stepCfg := range config.Pipeline {
		switch stepCfg.Type {
		case "capitalization":
			pipeline = append(pipeline, &CapStep{
				Fields: stepCfg.Fields,
			})

		case "address_normalizer":
			pipeline = append(pipeline, &AddressCleanStep{
				Fields: stepCfg.Fields,
			})

		case "link_validator":
			workers := stepCfg.Workers
			if workers <= 0 {
				workers = 10
			}
			pipeline = append(pipeline, &LinkValidatorStep{
				Fields:     stepCfg.Fields,
				MaxWorkers: workers,
			})

		case "empty_remover":
			action := stepCfg.Action
			if action == "" {
				action = "empty_field"
			}
			pipeline = append(pipeline, &EmptyRemoverStep{
				Fields: stepCfg.Fields,
				Action: action,
			})

		case "html_stripper":
			pipeline = append(pipeline, &HtmlStripStep{
				Fields: stepCfg.Fields,
			})

		case "phone_normalizer":
			pipeline = append(pipeline, &PhoneNormalizeStep{
				Fields: stepCfg.Fields,
			})

		case "field_dropper":
			pipeline = append(pipeline, &FieldDropStep{
				Fields: stepCfg.Fields,
			})

		case "whitespace_trimmer":
			pipeline = append(pipeline, &WhitespaceTrimStep{
				Fields: stepCfg.Fields,
			})

		case "email_remover":
			pipeline = append(pipeline, &EmailRemoveStep{
				Fields: stepCfg.Fields,
			})

		case "city_normalizer":
			pipeline = append(pipeline, &CityNormalizeStep{})

		default:
			return nil, fmt.Errorf("unknown pipeline step type: %q", stepCfg.Type)
		}
	}

	return pipeline, nil
}

// =============================================================================
// 3. PIPELINE STEP IMPLEMENTATIONS
// =============================================================================

// --- Feature: Capitalization ---

type CapStep struct {
	Fields []string
}

func (s *CapStep) Name() string { return "Capitalization" }

func (s *CapStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		for _, field := range s.Fields {
			if val, ok := record[field].(string); ok && val != "" {
				record[field] = customTitle(val)
			}
		}
	}
	return records, nil
}

func customTitle(s string) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return s
	}
	var b strings.Builder
	for i, w := range words {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(processWord(w))
	}
	return b.String()
}

func processWord(w string) string {
	idx := strings.IndexAny(w, "'")
	if idx == -1 {
		return titleCase(w)
	}

	prefix := w[:idx]
	suffix := w[idx+1:]
	prefix = strings.ToLower(prefix)
	suffix = titleCase(suffix)

	return prefix + "'" + suffix
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	for i := 1; i < len(runes); i++ {
		runes[i] = unicode.ToLower(runes[i])
	}
	return string(runes)
}

// --- Feature: Address Normalizer ---

type AddressCleanStep struct {
	Fields []string
}

var addressReplacements = []struct {
	re          *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`(?i)\b(ave|avenue)\b`), "avenue"},
	{regexp.MustCompile(`(?i)\b(r\.|rue)\b`), "rue"},
	{regexp.MustCompile(`(?i)\b(blvd|boulevard)\b`), "boulevard"},
}

func (s *AddressCleanStep) Name() string { return "Address Normalizer" }

func (s *AddressCleanStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		for _, field := range s.Fields {
			if val, ok := record[field].(string); ok && val != "" {
				cleanVal := strings.Join(strings.Fields(val), " ")
				for _, r := range addressReplacements {
					cleanVal = r.re.ReplaceAllString(cleanVal, r.replacement)
				}
				record[field] = cleanVal
			}
		}
	}
	return records, nil
}

// --- Feature: Empty Field Handler ---

type EmptyRemoverStep struct {
	Fields []string
	Action string // "delete_record" or "empty_field"
}

func (s *EmptyRemoverStep) Name() string { return "Empty Field Handler" }

func (s *EmptyRemoverStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	var finalRecords []map[string]interface{}

	for _, record := range records {
		shouldDelete := false

		for _, field := range s.Fields {
			val, exists := record[field]
			isEmpty := !exists || val == nil
			if !isEmpty {
				strVal, isStr := val.(string)
				if isStr && strings.TrimSpace(strVal) == "" {
					isEmpty = true
				}
			}

			if isEmpty {
				if s.Action == "delete_record" {
					shouldDelete = true
					break
				} else if s.Action == "empty_field" {
					record[field] = ""
				}
			} else {
				record[field] = ""
			}
		}

		if !shouldDelete {
			finalRecords = append(finalRecords, record)
		}
	}

	return finalRecords, nil
}

// --- Feature: HTML Stripper ---

type HtmlStripStep struct {
	Fields []string
}

func (s *HtmlStripStep) Name() string { return "HTML Stripper" }

var htmlTagRe = regexp.MustCompile(`<[^>]+>`)

func (s *HtmlStripStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		for _, field := range s.Fields {
			if val, ok := record[field].(string); ok && val != "" {
				stripped := htmlTagRe.ReplaceAllString(val, "")
				unescaped := html.UnescapeString(stripped)
				record[field] = strings.Join(strings.Fields(unescaped), " ")
			}
		}
	}
	return records, nil
}

// --- Feature: Phone Normalizer ---

type PhoneNormalizeStep struct {
	Fields []string
}

func (s *PhoneNormalizeStep) Name() string { return "Phone Normalizer" }

func (s *PhoneNormalizeStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		for _, field := range s.Fields {
			if val, ok := record[field].(string); ok && val != "" {
				clean := strings.ReplaceAll(val, " ", "")
				clean = strings.ReplaceAll(clean, "-", "")
				record[field] = clean
			}
		}
	}
	return records, nil
}

// --- Feature: Field Dropper ---

type FieldDropStep struct {
	Fields []string
}

func (s *FieldDropStep) Name() string { return "Field Dropper" }

func (s *FieldDropStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		for _, field := range s.Fields {
			delete(record, field)
		}
	}
	return records, nil
}

// --- Feature: Whitespace Trimmer ---

type WhitespaceTrimStep struct {
	Fields []string
}

func (s *WhitespaceTrimStep) Name() string { return "Whitespace Trimmer" }

func (s *WhitespaceTrimStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		for _, field := range s.Fields {
			if val, ok := record[field].(string); ok {
				record[field] = strings.TrimSpace(val)
			}
		}
	}
	return records, nil
}

// --- Feature: Email Remover ---

type EmailRemoveStep struct {
	Fields []string
}

var emailRegex = regexp.MustCompile(`(?i)[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)

func (s *EmailRemoveStep) Name() string { return "Email Remover" }

func (s *EmailRemoveStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		for _, field := range s.Fields {
			if val, ok := record[field].(string); ok && val != "" {
				cleanVal := emailRegex.ReplaceAllString(val, "")
				cleanVal = strings.Join(strings.Fields(cleanVal), " ")
				record[field] = cleanVal
			}
		}
	}
	return records, nil
}

// =============================================================================
// 4. MAIN APPLICATION ENTRY POINT
// =============================================================================

func main() {
	logger.Init(logger.Application)
	app := &cli.App{
		Name:  filepath.Base(os.Args[0]),
		Usage: "Config-driven JSON data normalization pipeline",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "in", Aliases: []string{"i"}, Value: "data.json", Usage: "Input JSON file"},
			&cli.StringFlag{Name: "out", Aliases: []string{"o"}, Value: "cleaned.json", Usage: "Output JSON file"},
			&cli.StringFlag{Name: "config", Aliases: []string{"c"}, Value: "cleanr-rules.yml", Usage: "Pipeline config YAML file"},
			&cli.BoolFlag{Name: "continue-on-error", Usage: "Continue pipeline execution if a step fails (default: abort)"},
		},
		Action: func(c *cli.Context) error {
			ctx := context.Background()

			pipeline, err := LoadConfig(c.String("config"))
			if err != nil {
				return fmt.Errorf("failed to load pipeline configuration: %w", err)
			}

			inFile := c.String("in")
			outFile := c.String("out")
			continueOnError := c.Bool("continue-on-error")

			data, err := os.ReadFile(inFile)
			if err != nil {
				return fmt.Errorf("read failed: %w", err)
			}

			var records []map[string]interface{}
			if err := json.Unmarshal(data, &records); err != nil {
				return fmt.Errorf("parse failed: %w", err)
			}

			for _, step := range pipeline {
				log.Info().Msgf("Running step: %s", step.Name())
				records, err = step.Process(ctx, records)
				if err != nil {
					if continueOnError {
						log.Warn().Err(err).Msgf("Step %q failed, continuing (data may be incomplete)", step.Name())
					} else {
						return fmt.Errorf("step %q failed: %w", step.Name(), err)
					}
				}
			}

			cleanedJSON, err := json.MarshalIndent(records, "", "  ")
			if err != nil {
				return fmt.Errorf("failed to marshal output JSON: %w", err)
			}
			if err := os.WriteFile(outFile, cleanedJSON, 0644); err != nil {
				return fmt.Errorf("write failed: %w", err)
			}

			log.Info().Msgf("Successfully wrote cleaned data to %s", outFile)
			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		log.Fatal().Err(err).Send()
	}
}
