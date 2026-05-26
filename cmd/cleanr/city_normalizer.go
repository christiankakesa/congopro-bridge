package main

import (
	"context"
	"regexp"
	"strings"

	"github.com/rs/zerolog/log"
)

// =============================================================================
// Static knowledge base
// =============================================================================

var cityCorrections = map[string]string{
	// Kinshasa
	"kinshasa": "Kinshasa",
	"kisnhasa": "Kinshasa",
	"kinshsa":  "Kinshasa",
	"knshasa":  "Kinshasa",

	// Lubumbashi
	"lubumbashi": "Lubumbashi",
	"lububmashi": "Lubumbashi",
	"lubumashi":  "Lubumbashi",

	// Brazzaville
	"brazzaville": "Brazzaville",
	"brazaville":  "Brazzaville",
	"brazzavlle":  "Brazzaville",
	"brazzavile":  "Brazzaville",

	// Pointe-Noire (many variants captured in the wild)
	"pointe-noire": "Pointe-Noire",
	"pointe noire": "Pointe-Noire",
	"pointe-noir":  "Pointe-Noire",
	"pointenoire":  "Pointe-Noire",
	"point-noire":  "Pointe-Noire",
	"point noire":  "Pointe-Noire",

	// Other DRC cities
	"matadi":    "Matadi",
	"goma":      "Goma",
	"bukavu":    "Bukavu",
	"mbujimayi": "Mbujimayi",
	"mbandaka":  "Mbandaka",
	"kolwezi":   "Kolwezi",
	"kisangani": "Kisangani",
	"butembo":   "Butembo",
	"likasi":    "Likasi",
	"kananga":   "Kananga",
	"kikwit":    "Kikwit",
	"boma":      "Boma",
	"muanda":    "Muanda",

	// Other ROC cities
	"dolisie": "Dolisie",
	"nkayi":   "N'Kayi",
	"n'kayi":  "N'Kayi",
}

var kinshasaCommunes = map[string]string{
	"barumbu":      "Barumbu",
	"bandalungwa":  "Bandalungwa",
	"bumbu":        "Bumbu",
	"gombe":        "Gombe",
	"kalamu":       "Kalamu",
	"kasa-vubu":    "Kasa-Vubu",
	"kasavubu":     "Kasa-Vubu",
	"kisenso":      "Kisenso",
	"kimbanseke":   "Kimbanseke",
	"kintambo":     "Kintambo",
	"kinshasa":     "Kinshasa", // the commune named Kinshasa (distinct from the city)
	"lemba":        "Lemba",
	"limete":       "Limete",
	"lingwala":     "Lingwala",
	"makala":       "Makala",
	"maluku":       "Maluku",
	"masina":       "Masina",
	"matete":       "Matete",
	"mont-ngafula": "Mont-Ngafula",
	"montngafula":  "Mont-Ngafula",
	"ngaba":        "Ngaba",
	"ngaliema":     "Ngaliema",
	"ndjili":       "Ndjili",
	"nsele":        "Nsele",
	"selembao":     "Selembao",
}

var brazzavilleCommunes = map[string]string{
	"makélékélé": "Makélékélé",
	"makelelele": "Makélékélé",
	"bacongo":    "Bacongo",
	"poto-poto":  "Poto-Poto",
	"potopoto":   "Poto-Poto",
	"moungali":   "Moungali",
	"ouenzé":     "Ouenzé",
	"ouenze":     "Ouenzé",
	"talangaï":   "Talangaï",
	"talangai":   "Talangaï",
	"mfilou":     "Mfilou",
	"madibou":    "Madibou",
}

var pointeNoireCommunes = map[string]string{
	"loandjili":     "Loandjili",
	"tié-tié":       "Tié-Tié",
	"tietie":        "Tié-Tié",
	"tie-tie":       "Tié-Tié",
	"ngoyo":         "Ngoyo",
	"mongo-mpoukou": "Mongo-Mpoukou",
	"mongompoukou":  "Mongo-Mpoukou",
	"mvou-mvou":     "Mvou-Mvou",
	"mvou mvou":     "Mvou-Mvou",
}

var allCommunes map[string]string

func init() {
	allCommunes = make(map[string]string, len(kinshasaCommunes)+len(brazzavilleCommunes)+len(pointeNoireCommunes))
	for k, v := range kinshasaCommunes {
		allCommunes[k] = v
	}
	for k, v := range brazzavilleCommunes {
		allCommunes[k] = v
	}
	for k, v := range pointeNoireCommunes {
		allCommunes[k] = v
	}
}

// =============================================================================
// Regular expressions
// =============================================================================

var bpPattern = regexp.MustCompile(`(?i)^\s*b\.?\s*p\.?\s*[\s\d]`)
var communePrefixPattern = regexp.MustCompile(`(?i)^\s*(?:commune\s+de\s+(?:la\s+|le\s+|l['']|les\s+)?|c/\s*)(.+)$`)

// =============================================================================
// CityNormalizeStep
// =============================================================================

type CityNormalizeStep struct{}

func (s *CityNormalizeStep) Name() string { return "City & Address Normalizer" }

func (s *CityNormalizeStep) Process(_ context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	for _, record := range records {
		s.normalizeCity(record)
		s.normalizeAddressLine2(record)
		s.migrateCommumeFromLine1(record)
	}
	return records, nil
}

// -----------------------------------------------------------------------------
// Step 1 – Normalise the city field
// -----------------------------------------------------------------------------

func (s *CityNormalizeStep) normalizeCity(record map[string]interface{}) {
	raw, ok := record["city"].(string)
	if !ok {
		return
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return
	}

	lower := strings.ToLower(trimmed)
	if canonical, found := cityCorrections[lower]; found {
		if trimmed != canonical {
			log.Debug().Msgf("[city_normalizer] city %q → %q", trimmed, canonical)
		}
		record["city"] = canonical
		return
	}

	if commune, found := kinshasaCommunes[lower]; found {
		log.Debug().Msgf("[city_normalizer] city %q is a Kinshasa commune → %q; promoting commune to address_line_2", trimmed, commune)
		record["city"] = "Kinshasa"
		if cur, _ := record["address_line_2"].(string); strings.TrimSpace(cur) == "" {
			record["address_line_2"] = commune
		}
		return
	}

	if strings.ContainsAny(trimmed, " ,") {
		if first := extractFirstKnownCity(trimmed); first != "" {
			log.Debug().Msgf("[city_normalizer] city %q contains phrase → extracted %q", trimmed, first)
			record["city"] = first
			return
		}
	}

	record["city"] = trimmed
}

func extractFirstKnownCity(value string) string {
	clean := regexp.MustCompile(`[,;/]+`).ReplaceAllString(value, " ")
	words := strings.Fields(clean)

	for i, w := range words {
		if canonical, ok := cityCorrections[strings.ToLower(w)]; ok {
			return canonical
		}

		if i+1 < len(words) {
			pair := strings.ToLower(w + " " + words[i+1])
			if canonical, ok := cityCorrections[pair]; ok {
				return canonical
			}
		}
	}
	return ""
}

// -----------------------------------------------------------------------------
// Step 2 – Normalise address_line_2
// -----------------------------------------------------------------------------

func (s *CityNormalizeStep) normalizeAddressLine2(record map[string]interface{}) {
	raw, ok := record["address_line_2"].(string)
	if !ok {
		return
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return
	}

	if bpPattern.MatchString(trimmed) {
		log.Debug().Msgf("[city_normalizer] address_line_2 %q is a P.O. box → cleared", trimmed)
		record["address_line_2"] = ""
		return
	}

	candidate := trimmed
	if m := communePrefixPattern.FindStringSubmatch(trimmed); m != nil {
		candidate = strings.TrimSpace(m[1])
	}

	if canonical, found := allCommunes[strings.ToLower(candidate)]; found {
		if trimmed != canonical {
			log.Debug().Msgf("[city_normalizer] address_line_2 %q → commune %q", trimmed, canonical)
		}
		record["address_line_2"] = canonical
		return
	}

	record["address_line_2"] = trimmed
}

// -----------------------------------------------------------------------------
// Step 3 – Migrate commune buried in address_line_1 into address_line_2
// -----------------------------------------------------------------------------

var communeInline = regexp.MustCompile(`(?i)(?:,\s*|[\s]+)(?:commune\s+de\s+(?:la\s+|le\s+|l['']|les\s+)?)?([A-ZÀ-Ö][a-zA-ZÀ-ÿ\-']+)$`)

func (s *CityNormalizeStep) migrateCommumeFromLine1(record map[string]interface{}) {
	line1, ok1 := record["address_line_1"].(string)
	if !ok1 || strings.TrimSpace(line1) == "" {
		return
	}

	line2, _ := record["address_line_2"].(string)
	if strings.TrimSpace(line2) != "" {
		return
	}

	candidate := strings.TrimSpace(line1)
	if m := communePrefixPattern.FindStringSubmatch(candidate); m != nil {
		candidate = strings.TrimSpace(m[1])
	}
	if canonical, found := allCommunes[strings.ToLower(candidate)]; found {
		log.Debug().Msgf("[city_normalizer] address_line_1 %q is a bare commune → migrated to address_line_2, field cleared", line1)
		record["address_line_1"] = ""
		record["address_line_2"] = canonical
		return
	}

	if m := communeInline.FindStringSubmatch(strings.TrimSpace(line1)); m != nil {
		token := strings.TrimSpace(m[1])
		if canonical, found := allCommunes[strings.ToLower(token)]; found {
			suffix := m[0]
			cleaned := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line1), suffix))
			log.Debug().Msgf("[city_normalizer] address_line_1 %q → stripped commune %q, migrated to address_line_2", line1, canonical)
			record["address_line_1"] = cleaned
			record["address_line_2"] = canonical
		}
	}
}
