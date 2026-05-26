package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/logger"
)

type Config struct {
	InputFile  string
	OutputFile string
	DelayMs    int
	Force      bool
	Minify     bool
}

func main() {
	logger.InitAuto()
	cfg := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Info().Msg("\nShutdown requested by user. Closing file gracefully...")
		cancel()
	}()

	inFile, err := os.Open(cfg.InputFile)
	if err != nil {
		log.Fatal().Msgf("Failed to open source file: %v", err)
	}
	defer inFile.Close()

	outFile, err := os.Create(cfg.OutputFile)
	if err != nil {
		log.Fatal().Msgf("Failed to create destination file: %v", err)
	}
	defer outFile.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	decoder := json.NewDecoder(inFile)

	t, err := decoder.Token()
	if err != nil || t != json.Delim('[') {
		log.Fatal().Msgf("JSON file must start with an array '['")
	}

	if cfg.Minify {
		outFile.WriteString("[")
	} else {
		outFile.WriteString("[\n")
	}

	count := 0
	updated := 0
	skipped := 0
	isFirst := true

	log.Info().Msg("Starting processing...")
	if cfg.DelayMs < 1000 {
		log.Warn().Msgf("⚠️  Warning: A delay of %dms may result in a ban on public OSM Nominatim servers.", cfg.DelayMs)
	}

	for decoder.More() {
		select {
		case <-ctx.Done():
			log.Info().Msg("Processing loop stopped.")
			goto Cleanup
		default:
		}

		var rec map[string]interface{}
		if err := decoder.Decode(&rec); err != nil {
			log.Error().Msgf("Decode error at index %d: %v", count, err)
			continue
		}
		count++

		if !cfg.Force && hasValidGeo(rec) {
			skipped++
			writeRecord(outFile, rec, &isFirst, cfg.Minify)
			continue
		}

		log.Info().Msgf("[%d] Processing: %v", count, rec["name"])

		lon, lat, err := resolveCoordinates(client, rec)
		if err != nil {
			log.Error().Msgf("  -> Geocoding failed: %v", err)
		} else {
			log.Info().Msgf("  -> Success: lon=%.6f, lat=%.6f", lon, lat)
			rec["geo"] = []interface{}{lon, lat}
			updated++
		}

		writeRecord(outFile, rec, &isFirst, cfg.Minify)

		time.Sleep(time.Duration(cfg.DelayMs) * time.Millisecond)
	}

Cleanup:
	if cfg.Minify {
		outFile.WriteString("]")
	} else {
		outFile.WriteString("\n]\n")
	}
	log.Info().Msgf("Done! Processed: %d | Updated: %d | Skipped (already valid): %d", count, updated, skipped)
}

func parseFlags() Config {
	var cfg Config
	flag.StringVar(&cfg.InputFile, "input", "cleaned_c.json", "Path to the source JSON file")
	flag.StringVar(&cfg.OutputFile, "output", "updated.json", "Path to the destination JSON file")
	flag.IntVar(&cfg.DelayMs, "delay", 1000, "Delay in milliseconds between requests (e.g. 250 = 4 requests/sec)")
	flag.BoolVar(&cfg.Force, "force", false, "Force geocoding even if coordinates already exist")
	flag.BoolVar(&cfg.Minify, "minify", false, "Minify the output JSON (disable indentation and newlines)")
	flag.Parse()
	return cfg
}

func writeRecord(w io.Writer, rec map[string]interface{}, isFirst *bool, minify bool) {
	var b []byte
	var err error

	if minify {
		b, err = json.Marshal(rec)
	} else {
		b, err = json.MarshalIndent(rec, "  ", "  ")
	}

	if err != nil {
		log.Error().Msgf("Failed to encode record: %v", err)
		return
	}

	if !*isFirst {
		if minify {
			w.Write([]byte(","))
		} else {
			w.Write([]byte(",\n  "))
		}
	} else {
		if !minify {
			w.Write([]byte("  "))
		}
		*isFirst = false
	}

	w.Write(b)
}

func hasValidGeo(rec map[string]interface{}) bool {
	geo, ok := rec["geo"].([]interface{})
	if !ok || len(geo) != 2 {
		return false
	}

	lon, ok1 := geo[0].(float64)
	lat, ok2 := geo[1].(float64)
	if ok1 && ok2 && (lon != 0 || lat != 0) {
		return true
	}
	return false
}

func resolveCoordinates(client *http.Client, rec map[string]interface{}) (float64, float64, error) {
	fullAddr := buildAddress(rec, true)
	if fullAddr != "" {
		lon, lat, err := geocodeWithRetry(client, fullAddr, 3)
		if err == nil {
			return lon, lat, nil
		}
		log.Warn().Msg("     [Info] Precise address not found, retrying at city level...")
	}

	fallbackAddr := buildAddress(rec, false)
	if fallbackAddr != "" && fallbackAddr != fullAddr {
		return geocodeWithRetry(client, fallbackAddr, 3)
	}

	return 0, 0, fmt.Errorf("no valid address found")
}

func buildAddress(rec map[string]interface{}, full bool) string {
	var parts []string
	if full {
		if v, ok := rec["address_line_1"].(string); ok && strings.TrimSpace(v) != "" {
			parts = append(parts, strings.TrimSpace(v))
		}
		if v, ok := rec["address_line_2"].(string); ok && strings.TrimSpace(v) != "" {
			parts = append(parts, strings.TrimSpace(v))
		}
	}
	if v, ok := rec["city"].(string); ok && strings.TrimSpace(v) != "" {
		parts = append(parts, strings.TrimSpace(v))
	}
	if v, ok := rec["country"].(string); ok && strings.TrimSpace(v) != "" {
		parts = append(parts, strings.TrimSpace(v))
	}
	return strings.Join(parts, ", ")
}

func geocodeWithRetry(client *http.Client, address string, maxRetries int) (float64, float64, error) {
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		lon, lat, err := geocode(client, address)
		if err == nil {
			return lon, lat, nil
		}
		lastErr = err

		if err.Error() == "no results found" {
			return 0, 0, err
		}

		time.Sleep(time.Duration(2*i+1) * time.Second)
	}
	return 0, 0, fmt.Errorf("after %d attempts: %v", maxRetries, lastErr)
}

func geocode(client *http.Client, address string) (float64, float64, error) {
	baseURL := "https://nominatim.openstreetmap.org/search"
	params := url.Values{}
	params.Set("q", address)
	params.Set("format", "json")
	params.Set("limit", "1")

	reqURL := baseURL + "?" + params.Encode()
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("User-Agent", "CongoproGeoUpdaterCLI/3.0 (https://congopro.com/help)")

	resp, err := client.Do(req)
	if err != nil {
		return 0, 0, fmt.Errorf("network error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, 0, fmt.Errorf("unexpected API status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	var results []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if err := json.Unmarshal(body, &results); err != nil {
		return 0, 0, err
	}
	if len(results) == 0 {
		return 0, 0, fmt.Errorf("no results found")
	}

	lat, err := strconv.ParseFloat(results[0].Lat, 64)
	if err != nil {
		return 0, 0, err
	}
	lon, err := strconv.ParseFloat(results[0].Lon, 64)
	if err != nil {
		return 0, 0, err
	}
	return lon, lat, nil
}
