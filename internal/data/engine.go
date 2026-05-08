// Package data encapsulates all indexing and search logic for Congopro Bridge.
//
// Tuning rationale (see inline comments for details):
//   - Analyser "fr" for French text fields; "keyword" for cities/countries.
//   - Name boost 18× > activity 20× > description 5× > address_line_2 4×.
//   - Fuzzy only for tokens > 6 chars (avoids false positives on short African names).
//   - embeddingDim 512 (sufficient for ~1 500-company corpus, half the RAM of 1024).
//   - chromemBatch 128 (limits peak RAM per batch to ~0.5 MB of float32 vectors).
//   - bleveWeight 0.60 / semanticWeight 0.40 (balanced for natural-language queries).
//   - Score floor 0.05 filters noise from near-zero combined scores.
//   - slugMap provides O(1) lookup for company-detail page requests.
//   - sync.Once + IndexingDone channel: safe concurrent startup.
package data

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"math"
	"net/http"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"

	bleve "github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/search/query"
	chromem "github.com/philippgille/chromem-go"

	_ "github.com/blevesearch/bleve/v2/analysis/lang/en"
	"github.com/blevesearch/bleve/v2/analysis/lang/fr"
)

//go:embed cleaned_c.json
var CompaniesJSON []byte

// ─────────────────────────────────────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────────────────────────────────────

const (
	fieldName         = "name"
	fieldActivity     = "activity"
	fieldAddress      = "address"
	fieldAddressLine2 = "address_line_2"
	fieldCity         = "city"
	fieldCountry      = "country"
	fieldDescription  = "description"
	fieldSlogan       = "slogan"
	fieldLocation     = "location"

	MaxResults     = 30
	bleveWeight    = 0.60
	semanticWeight = 0.40
	embeddingDim   = 512
	chromemBatch   = 128
	bleveBatch     = 500
	scoreFloor     = 0.05
	fuzzyMinLen    = 6

	ollamaURL = "http://localhost:11434/api/generate"
	aiModel   = "gemma:2b"

	boostName         = 18.0
	boostActivity     = 20.0
	boostDescription  = 5.0
	boostAddress      = 3.0
	boostAddressLine2 = 4.0
	boostCity         = 2.0
	boostSlogan       = 0.5
)

// Global replacer compiled ONCE for performance.
var punctuationReplacer = strings.NewReplacer(
	"'", " ",
	"’", " ",
	"\"", " ",
	"-", " ",
)

var frStopWords = map[string]bool{
	"le": true, "la": true, "les": true, "de": true, "des": true, "un": true, "une": true,
	"et": true, "à": true, "a": true, "il": true, "elle": true, "en": true, "pour": true,
	"par": true, "dans": true, "sur": true, "au": true, "aux": true, "du": true, "qui": true,
	"que": true, "quoi": true, "dont": true, "où": true, "ou": true, "montre": true, "moi": true,
	"est": true, "sont": true, "ce": true, "ces": true, "avec": true, "je": true, "cherche": true,
	"trouve": true, "l": true, "d": true, "qu": true, "m": true, "s": true, "t": true,
}

var geoNoise = map[string]bool{
	"avenue":    true,
	"rue":       true,
	"boulevard": true,
	"quartier":  true,
	"commune":   true,
}

// ─────────────────────────────────────────────────────────────────────────────
// MongoDB JSON helpers & Domain Models
// ─────────────────────────────────────────────────────────────────────────────

type MongoOID struct{ Value string }

func (m *MongoOID) UnmarshalJSON(b []byte) error {
	var w struct {
		OID string `json:"$oid"`
	}
	if err := json.Unmarshal(b, &w); err == nil && w.OID != "" {
		m.Value = w.OID
		return nil
	}
	return json.Unmarshal(b, &m.Value)
}

type MongoDate struct{ Value time.Time }

func (m *MongoDate) UnmarshalJSON(b []byte) error {
	var w struct {
		Date string `json:"$date"`
	}
	if err := json.Unmarshal(b, &w); err == nil && w.Date != "" {
		t, err := time.Parse(time.RFC3339Nano, w.Date)
		if err != nil {
			return fmt.Errorf("MongoDate parse %q: %w", w.Date, err)
		}
		m.Value = t
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("MongoDate fallback parse %q: %w", s, err)
	}
	m.Value = t
	return nil
}

type GeoLocation struct {
	Lon float64 `json:"lon"`
	Lat float64 `json:"lat"`
}

type rawCompany struct {
	ID           MongoOID  `json:"_id"`
	Name         string    `json:"name"`
	NameSeo      string    `json:"name_seo"`
	Activity     string    `json:"activity"`
	City         string    `json:"city"`
	Country      string    `json:"country"`
	Description  string    `json:"description"`
	Slogan       string    `json:"slogan"`
	Website      string    `json:"website"`
	Email        string    `json:"email"`
	MainPhone    string    `json:"main_phone"`
	AddressLine  string    `json:"address_line_1"`
	AddressLine2 string    `json:"address_line_2"`
	Published    bool      `json:"published"`
	UpdatedAt    MongoDate `json:"updated_at"`
	StatsShow    int       `json:"stats_show"`
	Geo          []float64 `json:"geo"`
}

type Company struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	NameSeo      string       `json:"name_seo"`
	Activity     string       `json:"activity"`
	City         string       `json:"city"`
	Country      string       `json:"country"`
	Description  string       `json:"description"`
	Slogan       string       `json:"slogan"`
	Website      string       `json:"website"`
	Email        string       `json:"email"`
	Phone        string       `json:"phone"`
	Address      string       `json:"address"`
	AddressLine2 string       `json:"address_line_2"`
	UpdatedAt    time.Time    `json:"updated_at"`
	StatsShow    int          `json:"stats_show"`
	Location     *GeoLocation `json:"location,omitempty"`
}

type SearchResult struct {
	Company
	Score float64 `json:"score"`
}

type ollamaRequest struct {
	Model   string                 `json:"model"`
	Prompt  string                 `json:"prompt"`
	Stream  bool                   `json:"stream"`
	Options map[string]interface{} `json:"options"`
}

type ollamaResponse struct {
	Response string `json:"response"`
}

// ─────────────────────────────────────────────────────────────────────────────
// Engine
// ─────────────────────────────────────────────────────────────────────────────

type Engine struct {
	initOnce     sync.Once
	IndexingDone chan struct{}

	mu         sync.RWMutex
	companies  []Company
	companyMap map[string]*Company
	slugMap    map[string]*Company

	bleveIdx    bleve.Index
	chromemColl *chromem.Collection

	vocab    []string
	vocabMap map[string]int
}

func NewEngine() *Engine {
	return &Engine{
		IndexingDone: make(chan struct{}),
		companyMap:   make(map[string]*Company),
		slugMap:      make(map[string]*Company),
	}
}

func (e *Engine) Companies() []Company {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.companies
}

// ─────────────────────────────────────────────────────────────────────────────
// Text Processing Pipeline
// ─────────────────────────────────────────────────────────────────────────────

var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func normalizeForBleve(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	result, _, _ := transform.String(t, s)
	return strings.ToLower(result)
}

func normalizeToken(tok string) string {
	tok = normalizeForBleve(tok)
	// Conservative plural handling
	if strings.HasSuffix(tok, "s") && len(tok) > 4 && !strings.HasSuffix(tok, "ss") && !strings.HasSuffix(tok, "us") {
		tok = strings.TrimSuffix(tok, "s")
	}
	return tok
}

// cleanAndSplit normalizes text, replaces punctuation, and returns clean tokens.
func cleanAndSplit(s string) []string {
	s = normalizeForBleve(s)
	s = punctuationReplacer.Replace(s)

	parts := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	clean := make([]string, 0, len(parts))
	for _, w := range parts {
		if len(w) > 2 && !frStopWords[w] {
			clean = append(clean, w)
		}
	}
	return clean
}

// tokenize is used for TF-IDF. It KEEPS duplicate words to count term frequency.
func tokenize(s string) []string {
	return cleanAndSplit(s)
}

// extractKeywords is used for Bleve. It REMOVES duplicate words to build clean query boolean logic.
func extractKeywords(q string) []string {
	rawTokens := cleanAndSplit(q)
	keywords := make([]string, 0, len(rawTokens))
	seen := make(map[string]struct{}, len(rawTokens))

	for _, token := range rawTokens {
		token = normalizeToken(token)
		if _, exists := seen[token]; exists {
			continue
		}
		seen[token] = struct{}{}
		keywords = append(keywords, token)
	}
	return keywords
}

// ─────────────────────────────────────────────────────────────────────────────
// Local TF-IDF Vectorization
// ─────────────────────────────────────────────────────────────────────────────

func (e *Engine) buildVocab() {
	freq := make(map[string]int, 8192)
	for _, c := range e.companies {
		text := strings.Join([]string{
			c.Name, c.Activity, c.Address, c.AddressLine2,
			c.City, c.Country, c.Description,
		}, " ")
		for _, tok := range tokenize(text) {
			freq[tok]++
		}
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(freq))
	for k, v := range freq {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].v > pairs[j].v })

	dim := embeddingDim
	if len(pairs) < dim {
		dim = len(pairs)
	}
	e.vocab = make([]string, 0, dim)
	e.vocabMap = make(map[string]int, dim)
	for i := 0; i < dim; i++ {
		e.vocab = append(e.vocab, pairs[i].k)
		e.vocabMap[pairs[i].k] = i
	}
	log.Printf("[embed] vocabulary built — %d dimensions (corpus tokens: %d)", dim, len(pairs))
}

func (e *Engine) embed(text string) []float32 {
	vec := make([]float32, embeddingDim)
	for _, tok := range tokenize(text) {
		idx, ok := e.vocabMap[tok]
		if !ok || idx < 0 || idx >= len(vec) {
			continue
		}
		vec[idx] += 1.0
	}

	var norm float64
	for _, v := range vec {
		norm += float64(v * v)
	}
	if norm == 0 {
		return vec
	}

	norm = math.Sqrt(norm)
	inv := float32(1.0 / norm)
	for i := range vec {
		vec[i] *= inv
	}
	return vec
}

// ─────────────────────────────────────────────────────────────────────────────
// Indexing Lifecycle
// ─────────────────────────────────────────────────────────────────────────────

func (e *Engine) LoadAndIndex() error {
	var loadErr error
	e.initOnce.Do(func() {
		loadErr = e.loadAndIndexOnce()
		if loadErr == nil {
			close(e.IndexingDone)
		}
	})
	return loadErr
}

func (e *Engine) loadAndIndexOnce() error {
	start := time.Now()

	var raws []rawCompany
	if err := json.Unmarshal(CompaniesJSON, &raws); err != nil {
		return fmt.Errorf("unmarshal companies: %w", err)
	}
	log.Printf("[load] parsed %d raw companies", len(raws))

	companies := make([]Company, 0, len(raws))
	seenIDs := make(map[string]struct{}, len(raws))
	dupCount := 0

	for i, r := range raws {
		id := r.ID.Value
		if id == "" {
			id = fmt.Sprintf("gen-%d", i)
		}
		if _, exists := seenIDs[id]; exists {
			dupCount++
			continue
		}
		seenIDs[id] = struct{}{}

		c := Company{
			ID:           id,
			Name:         r.Name,
			NameSeo:      r.NameSeo,
			Activity:     r.Activity,
			City:         r.City,
			Country:      r.Country,
			Description:  stripHTML(r.Description),
			Slogan:       r.Slogan,
			Website:      r.Website,
			Email:        r.Email,
			Phone:        r.MainPhone,
			Address:      r.AddressLine,
			AddressLine2: r.AddressLine2,
			UpdatedAt:    r.UpdatedAt.Value,
			StatsShow:    r.StatsShow,
		}
		if len(r.Geo) == 2 {
			c.Location = &GeoLocation{Lon: r.Geo[0], Lat: r.Geo[1]}
		}
		companies = append(companies, c)
	}

	companyMap := make(map[string]*Company, len(companies))
	slugMap := make(map[string]*Company, len(companies))
	for i := range companies {
		ptr := &companies[i]
		companyMap[ptr.ID] = ptr
		if ptr.NameSeo != "" {
			if _, collision := slugMap[ptr.NameSeo]; !collision {
				slugMap[ptr.NameSeo] = ptr
			}
		}
	}

	e.mu.Lock()
	e.companies = companies
	e.companyMap = companyMap
	e.slugMap = slugMap
	e.mu.Unlock()

	raws = nil
	runtime.GC()

	e.buildVocab()

	idx, err := buildBleveMapping()
	if err != nil {
		return err
	}
	e.bleveIdx = idx
	if err := e.indexBleve(); err != nil {
		return err
	}

	if err := e.indexChromem(); err != nil {
		return err
	}

	log.Printf("[load] all systems ready in %s (%d companies indexed)", time.Since(start).Round(time.Millisecond), len(companies))
	return nil
}

func buildBleveMapping() (bleve.Index, error) {
	mapping := bleve.NewIndexMapping()
	docMap := bleve.NewDocumentMapping()

	frAnalyzer := fr.AnalyzerName
	if frAnalyzer == "" {
		frAnalyzer = "standard"
	}

	frText := bleve.NewTextFieldMapping()
	frText.Analyzer = frAnalyzer
	keyword := bleve.NewKeywordFieldMapping()
	geo := bleve.NewGeoPointFieldMapping()

	docMap.AddFieldMappingsAt(fieldName, frText)
	docMap.AddFieldMappingsAt(fieldActivity, frText)
	docMap.AddFieldMappingsAt(fieldAddress, frText)
	docMap.AddFieldMappingsAt(fieldAddressLine2, frText)
	docMap.AddFieldMappingsAt(fieldCity, keyword)
	docMap.AddFieldMappingsAt(fieldCountry, keyword)
	docMap.AddFieldMappingsAt(fieldDescription, frText)
	docMap.AddFieldMappingsAt(fieldSlogan, frText)
	docMap.AddFieldMappingsAt(fieldLocation, geo)

	mapping.DefaultMapping = docMap
	mapping.DefaultAnalyzer = frAnalyzer

	return bleve.NewMemOnly(mapping)
}

func (e *Engine) indexBleve() error {
	batch := e.bleveIdx.NewBatch()
	total := len(e.companies)

	for i := range e.companies {
		c := &e.companies[i]
		doc := map[string]interface{}{
			fieldName:         c.Name,
			fieldActivity:     c.Activity,
			fieldAddress:      c.Address,
			fieldAddressLine2: c.AddressLine2,
			fieldCity:         c.City,
			fieldCountry:      c.Country,
			fieldDescription:  c.Description,
			fieldSlogan:       c.Slogan,
		}
		if c.Location != nil {
			doc[fieldLocation] = map[string]float64{
				"lat": c.Location.Lat,
				"lon": c.Location.Lon,
			}
		}
		_ = batch.Index(c.ID, doc)

		if (i+1)%bleveBatch == 0 || i == total-1 {
			if err := e.bleveIdx.Batch(batch); err != nil {
				return err
			}
			batch = e.bleveIdx.NewBatch()
		}
	}
	return nil
}

func (e *Engine) indexChromem() error {
	db := chromem.NewDB()
	embFn := chromem.EmbeddingFunc(func(ctx context.Context, text string) ([]float32, error) {
		return e.embed(text), nil
	})

	coll, err := db.CreateCollection("companies", nil, embFn)
	if err != nil {
		return err
	}

	total := len(e.companies)
	for start := 0; start < total; start += chromemBatch {
		end := start + chromemBatch
		if end > total {
			end = total
		}

		docs := make([]chromem.Document, 0, end-start)
		for _, c := range e.companies[start:end] {
			text := strings.Join([]string{
				c.Name, c.Name, // Repeated intentionally for vector weight
				c.Activity, c.Activity,
				c.Description, c.Slogan,
				c.Address, c.AddressLine2,
				c.City, c.Country,
			}, " ")

			docs = append(docs, chromem.Document{
				ID:      c.ID,
				Content: text,
				Metadata: map[string]string{
					fieldName:     c.Name,
					fieldActivity: c.Activity,
					fieldCity:     c.City,
					fieldCountry:  c.Country,
				},
			})
		}
		if err := coll.AddDocuments(context.Background(), docs, 4); err != nil {
			return err
		}
	}
	e.chromemColl = coll
	return nil
}

// ─────────────────────────────────────────────────────────────────────────────
// HybridSearch Logic
// ─────────────────────────────────────────────────────────────────────────────

func (e *Engine) HybridSearch(q string) ([]SearchResult, error) {
	e.mu.RLock()
	bleveIdx := e.bleveIdx
	chromemColl := e.chromemColl
	e.mu.RUnlock()

	if bleveIdx == nil || chromemColl == nil {
		return nil, fmt.Errorf("search engines not fully initialised")
	}

	q = strings.TrimSpace(q)
	if q == "" {
		return []SearchResult{}, nil
	}

	const slugPrefix = "-company-slug:"
	if strings.HasPrefix(q, slugPrefix) {
		slug := strings.TrimSpace(strings.TrimPrefix(q, slugPrefix))
		e.mu.RLock()
		defer e.mu.RUnlock()
		if c, found := e.slugMap[slug]; found {
			return []SearchResult{{Company: *c, Score: 1.0}}, nil
		}
		return []SearchResult{}, nil
	}

	type result struct {
		hits map[string]float64
		err  error
	}

	bleveCh := make(chan result, 1)
	semCh := make(chan result, 1)

	go func() {
		h, err := e.runBleveSearch(q)
		bleveCh <- result{hits: h, err: err}
	}()

	go func() {
		h, err := e.runChromemSearch(q)
		semCh <- result{hits: h, err: err}
	}()

	br := <-bleveCh
	sr := <-semCh

	if br.err != nil {
		return nil, fmt.Errorf("bleve search: %w", br.err)
	}

	merged := make(map[string]float64, len(br.hits)+len(sr.hits))
	for id, s := range br.hits {
		merged[id] += bleveWeight * s
	}
	for id, s := range sr.hits {
		merged[id] += semanticWeight * s
	}

	// OPIMIZATION: Apply exact-match boost ONLY to companies that were actually
	// returned by the search engines (O(Found) instead of O(Total Corpus)).
	normQ := normalizeForBleve(q)
	e.mu.RLock()
	for id := range merged {
		if c, ok := e.companyMap[id]; ok {
			if normalizeForBleve(c.Name) == normQ {
				merged[id] += 0.35
			}
		}
	}
	e.mu.RUnlock()

	type idScore struct {
		id    string
		score float64
	}
	ranked := make([]idScore, 0, len(merged))

	for id, s := range merged {
		if s >= scoreFloor {
			ranked = append(ranked, idScore{id: id, score: s})
		}
	}

	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	e.mu.RLock()
	defer e.mu.RUnlock()

	results := make([]SearchResult, 0, MaxResults)
	for _, is := range ranked {
		if len(results) >= MaxResults {
			break
		}
		if c, ok := e.companyMap[is.id]; ok {
			results = append(results, SearchResult{
				Company: *c,
				Score:   math.Round(is.score*1000) / 1000,
			})
		}
	}

	return results, nil
}

func fieldQuery(token, field string, boost float64) query.Query {
	cleanToken := normalizeToken(token)

	mq := bleve.NewMatchQuery(cleanToken)
	mq.SetField(field)
	mq.SetBoost(boost)

	combined := bleve.NewBooleanQuery()
	combined.AddShould(mq)

	if len(cleanToken) >= 3 {
		pq := bleve.NewPrefixQuery(cleanToken)
		pq.SetField(field)
		pq.SetBoost(boost * 0.7)
		combined.AddShould(pq)
	}

	if len(cleanToken) > fuzzyMinLen {
		fq := bleve.NewFuzzyQuery(cleanToken)
		fq.SetField(field)
		fq.SetFuzziness(1)
		fq.SetBoost(boost * 0.4)
		combined.AddShould(fq)
	}

	return combined
}

func (e *Engine) runBleveSearch(q string) (map[string]float64, error) {
	tokens := extractKeywords(q)
	if len(tokens) == 0 {
		return map[string]float64{}, nil
	}

	topQ := bleve.NewBooleanQuery()

	for _, token := range tokens {
		tokenQ := bleve.NewBooleanQuery()
		tokenQ.AddShould(fieldQuery(token, fieldName, boostName))
		tokenQ.AddShould(fieldQuery(token, fieldActivity, boostActivity))
		tokenQ.AddShould(fieldQuery(token, fieldAddressLine2, boostAddressLine2))
		tokenQ.AddShould(fieldQuery(token, fieldCity, boostCity))
		tokenQ.AddShould(fieldQuery(token, fieldDescription, boostDescription))
		tokenQ.AddShould(fieldQuery(token, fieldCountry, boostCity))
		tokenQ.AddShould(fieldQuery(token, fieldSlogan, boostSlogan))

		addressBoost := boostAddress
		if geoNoise[token] {
			addressBoost *= 0.3
		}
		tokenQ.AddShould(fieldQuery(token, fieldAddress, addressBoost))

		tokenQ.SetMinShould(1)
		topQ.AddShould(tokenQ)
	}

	minShould := 1
	if len(tokens) >= 3 {
		minShould = 2
	}
	if len(tokens) >= 5 {
		minShould = 3
	}
	topQ.SetMinShould(float64(minShould))

	cleanedPhrase := strings.Join(tokens, " ")
	phraseQ := bleve.NewMatchPhraseQuery(cleanedPhrase)
	phraseQ.SetField(fieldName)
	phraseQ.SetBoost(10.0)

	finalQuery := bleve.NewDisjunctionQuery(topQ, phraseQ)
	finalQuery.SetMin(1)

	req := bleve.NewSearchRequestOptions(finalQuery, MaxResults*2, 0, false)
	res, err := e.bleveIdx.Search(req)
	if err != nil {
		return nil, err
	}

	var maxScore float64
	for _, hit := range res.Hits {
		if hit.Score > maxScore {
			maxScore = hit.Score
		}
	}

	hits := make(map[string]float64, len(res.Hits))
	if maxScore <= 0 {
		return hits, nil
	}

	inv := 1.0 / maxScore
	for _, hit := range res.Hits {
		hits[hit.ID] = hit.Score * inv
	}
	return hits, nil
}

func (e *Engine) runChromemSearch(q string) (map[string]float64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	q = normalizeForBleve(q)
	res, err := e.chromemColl.Query(ctx, q, MaxResults*2, nil, nil)
	if err != nil {
		return nil, err
	}

	var maxSim float32
	for _, r := range res {
		if r.Similarity > maxSim {
			maxSim = r.Similarity
		}
	}

	hits := make(map[string]float64, len(res))
	if maxSim <= 0 {
		return hits, nil
	}

	inv := 1.0 / float64(maxSim)
	for _, r := range res {
		score := float64(r.Similarity) * inv
		if math.IsNaN(score) || math.IsInf(score, 0) {
			continue
		}
		hits[r.ID] = score
	}
	return hits, nil
}

// ─────────────────────────────────────────────────────────────────────────────
// RAG Pipeline
// ─────────────────────────────────────────────────────────────────────────────

func (e *Engine) GenerateAnswer(userQuery string, topResults []SearchResult) (string, error) {
	if len(topResults) == 0 {
		return "Désolé, je n'ai trouvé aucune entreprise pertinente pour répondre à votre question.", nil
	}

	limit := 5
	if len(topResults) < limit {
		limit = len(topResults)
	}

	var sb strings.Builder
	sb.Grow(2048)
	sb.WriteString(`Tu es l'assistant IA de Congopro Bridge.
Ta mission est de répondre à la question de l'utilisateur en utilisant UNIQUEMENT les informations des entreprises ci-dessous.
Si l'information n'est pas dans le texte, dis que tu ne sais pas. Sois concis, professionnel et direct.
Ne propose pas de services concurrents.

CONTEXTE (Entreprises trouvées) :
`)
	for i := 0; i < limit; i++ {
		c := topResults[i].Company
		desc := []rune(c.Description)
		if len(desc) > 150 {
			desc = append(desc[:150], '…')
		}
		addr := c.Address
		if c.AddressLine2 != "" {
			addr += ", " + c.AddressLine2
		}
		fmt.Fprintf(&sb,
			"- Nom: %s\n  Activité: %s\n  Adresse: %s\n  Ville: %s\n  Description: %s\n\n",
			c.Name, c.Activity, addr, c.City, string(desc),
		)
	}
	sb.WriteString("QUESTION DE L'UTILISATEUR :\n")
	sb.WriteString(userQuery)
	sb.WriteString("\n\nRÉPONSE :")

	body, _ := json.Marshal(ollamaRequest{
		Model:  aiModel,
		Prompt: sb.String(),
		Stream: false,
		Options: map[string]interface{}{
			"num_predict": 150,
			"temperature": 0.2,
			"num_thread":  2,
		},
	})

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Post(ollamaURL, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return "", fmt.Errorf("connexion Ollama: %w", err)
	}
	defer resp.Body.Close()

	var out ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode Ollama response: %w", err)
	}
	return out.Response, nil
}
