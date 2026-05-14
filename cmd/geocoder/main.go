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
	logger.Init(true)
	cfg := parseFlags()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		log.Info().Msg("\nArrêt demandé par l'utilisateur. Clôture propre du fichier en cours...")
		cancel()
	}()

	inFile, err := os.Open(cfg.InputFile)
	if err != nil {
		log.Fatal().Msgf("Échec de l'ouverture du fichier source: %v", err)
	}
	defer inFile.Close()

	outFile, err := os.Create(cfg.OutputFile)
	if err != nil {
		log.Fatal().Msgf("Échec de la création du fichier de destination: %v", err)
	}
	defer outFile.Close()

	client := &http.Client{Timeout: 10 * time.Second}
	decoder := json.NewDecoder(inFile)

	t, err := decoder.Token()
	if err != nil || t != json.Delim('[') {
		log.Fatal().Msgf("Le fichier JSON doit commencer par un tableau '['")
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

	log.Info().Msg("Début du traitement...")
	if cfg.DelayMs < 1000 {
		log.Warn().Msgf("⚠️ Attention: Un délai de %dms peut entraîner un bannissement sur les serveurs publics OSM Nominatim.", cfg.DelayMs)
	}

	for decoder.More() {
		select {
		case <-ctx.Done():
			log.Info().Msg("Arrêt de la boucle de traitement.")
			goto Cleanup
		default:
		}

		var rec map[string]interface{}
		if err := decoder.Decode(&rec); err != nil {
			log.Error().Msgf("Erreur de décodage à l'index %d: %v", count, err)
			continue
		}
		count++

		if !cfg.Force && hasValidGeo(rec) {
			skipped++
			writeRecord(outFile, rec, &isFirst, cfg.Minify)
			continue
		}

		log.Info().Msgf("[%d] Traitement: %v", count, rec["name"])

		lon, lat, err := resolveCoordinates(client, rec)
		if err != nil {
			log.Error().Msgf("  -> Échec géocodage: %v", err)
		} else {
			log.Info().Msgf("  -> Succès: lon=%.6f, lat=%.6f", lon, lat)
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
	log.Info().Msgf("Terminé ! Traités: %d | Mis à jour: %d | Ignorés (déjà ok): %d", count, updated, skipped)
}

func parseFlags() Config {
	var cfg Config
	flag.StringVar(&cfg.InputFile, "input", "cleaned_c.json", "Chemin vers le fichier JSON source")
	flag.StringVar(&cfg.OutputFile, "output", "updated.json", "Chemin vers le fichier JSON de destination")
	flag.IntVar(&cfg.DelayMs, "delay", 1000, "Délai en millisecondes entre les requêtes (ex: 250 = 4 requêtes/sec)")
	flag.BoolVar(&cfg.Force, "force", false, "Forcer le géocodage même si les coordonnées existent déjà")
	flag.BoolVar(&cfg.Minify, "minify", false, "Minifier le JSON de sortie (désactiver l'indentation et les retours à la ligne)")
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
		log.Error().Msgf("Erreur d'encodage du record: %v", err)
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
		log.Warn().Msg("     [Info] Adresse précise introuvable, essai au niveau de la ville...")
	}

	fallbackAddr := buildAddress(rec, false)
	if fallbackAddr != "" && fallbackAddr != fullAddr {
		return geocodeWithRetry(client, fallbackAddr, 3)
	}

	return 0, 0, fmt.Errorf("aucune adresse valide trouvée")
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

		if err.Error() == "aucun résultat" {
			return 0, 0, err
		}

		time.Sleep(time.Duration(2*i+1) * time.Second)
	}
	return 0, 0, fmt.Errorf("après %d tentatives: %v", maxRetries, lastErr)
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
		return 0, 0, fmt.Errorf("erreur réseau: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return 0, 0, fmt.Errorf("statut API invalide: %d", resp.StatusCode)
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
		return 0, 0, fmt.Errorf("aucun résultat")
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
