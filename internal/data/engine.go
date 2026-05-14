package data

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/blevesearch/bleve/v2/mapping"
	chromem "github.com/philippgille/chromem-go"
	"github.com/rs/zerolog/log"

	"congopro-bridge/internal/config"
)

//go:embed cleaned_c.json
var CompaniesJSON []byte

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

	MaxResults   = 30
	chromemBatch = 128
	bleveBatch   = 500
)

var geoCintyCountryAliases = map[string]bool{
	"congo":    true,
	"rdc":      true,
	"drc":      true,
	"kinshasa": true,
}

var removeNonSpacingMarks = runes.Remove(runes.In(unicode.Mn))

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

type ollamaEmbedRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaEmbedResponse struct {
	Embedding []float32 `json:"embedding"`
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

type SitemapEntry struct {
	Loc        string
	LastMod    time.Time
	ChangeFreq string
	Priority   float64
}

type Engine struct {
	Config *config.Config

	initOnce     sync.Once
	IndexingDone chan struct{}

	mu         sync.RWMutex
	companies  []Company
	companyMap map[string]*Company
	slugMap    map[string]*Company

	bleveIdx    bleve.Index
	chromemColl *chromem.Collection
	httpClient  *http.Client

	SitemapCache []byte
	SitemapMu    sync.RWMutex

	knownCities map[string]bool
}

func NewEngine(cfg *config.Config) *Engine {
	return &Engine{
		IndexingDone: make(chan struct{}),
		companyMap:   make(map[string]*Company),
		slugMap:      make(map[string]*Company),
		Config:       cfg,
		httpClient:   &http.Client{},
	}
}

func (e *Engine) Companies() []Company {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.companies
}

var htmlTagRE = regexp.MustCompile(`<[^>]+>`)

func stripHTML(s string) string {
	s = htmlTagRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

func normalizeForSearch(s string) string {
	t := transform.Chain(norm.NFD, removeNonSpacingMarks, norm.NFC)
	result, _, _ := transform.String(t, s)
	return strings.ToLower(strings.TrimSpace(result))
}

func extractGeoTokens(q string, knownCities map[string]bool) (geoTokens []string, activityQ string) {
	words := strings.Fields(normalizeForSearch(q))
	var activity []string
	for _, w := range words {
		if len([]rune(w)) <= 2 {
			continue
		}
		if knownCities[w] {
			geoTokens = append(geoTokens, w)
		} else {
			activity = append(activity, w)
		}
	}
	return geoTokens, strings.Join(activity, " ")
}

func reciprocalRankFusion(rankings ...map[string]int) map[string]float64 {
	const k = 60
	scores := make(map[string]float64)
	for _, ranking := range rankings {
		for id, rank := range ranking {
			scores[id] += 1.0 / float64(k+rank)
		}
	}
	return scores
}

func hitsToRanking(hits map[string]float64) map[string]int {
	type kv struct {
		id    string
		score float64
	}
	sorted := make([]kv, 0, len(hits))
	for id, s := range hits {
		sorted = append(sorted, kv{id, s})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].score > sorted[j].score
	})
	ranking := make(map[string]int, len(sorted))
	for i, kv := range sorted {
		ranking[kv.id] = i + 1
	}
	return ranking
}

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
	log.Info().Msgf("[load] parsed %d raw companies", len(raws))

	companies := make([]Company, 0, len(raws))
	seenIDs := make(map[string]struct{}, len(raws))

	for i, r := range raws {
		id := r.ID.Value
		if id == "" {
			id = fmt.Sprintf("gen-%d", i)
		}
		if _, exists := seenIDs[id]; exists {
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
	knownCities := make(map[string]bool, 100)
	for i := range companies {
		ptr := &companies[i]
		companyMap[ptr.ID] = ptr
		if ptr.NameSeo != "" {
			if _, collision := slugMap[ptr.NameSeo]; !collision {
				slugMap[ptr.NameSeo] = ptr
			}
		}

		if ptr.City != "" {
			knownCities[normalizeForSearch(ptr.City)] = true
		}
		if ptr.Country != "" {
			knownCities[normalizeForSearch(ptr.Country)] = true
		}
	}

	for k, v := range geoCintyCountryAliases {
		knownCities[k] = v
	}

	e.mu.Lock()
	e.companies = companies
	e.companyMap = companyMap
	e.slugMap = slugMap
	e.knownCities = knownCities
	e.mu.Unlock()

	raws = nil
	runtime.GC()

	log.Info().Msgf("[load] connecting to Ollama embedding model: %s", e.Config.EmbeddingModel)

	indexPath := filepath.Join(e.Config.ModelsDir, "bleve.idx")

	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		log.Info().Msg("[load] building Bleve text index from scratch (this will take a moment)...")

		mapping, _ := buildBleveMapping()
		idx, err := bleve.New(indexPath, mapping)
		if err != nil {
			return err
		}
		e.bleveIdx = idx

		if err := e.indexBleve(); err != nil {
			return err
		}
	} else {
		log.Info().Msg("[load] loading existing Bleve text index from disk (fast)...")

		idx, err := bleve.Open(indexPath)
		if err != nil {
			return err
		}
		e.bleveIdx = idx
	}

	if err := e.pingOllama(); err != nil {
		return fmt.Errorf("ollama not reachable: %w", err)
	}

	chromemPath := filepath.Join(e.Config.ModelsDir, "chromem.db")
	if err := e.indexSem(chromemPath); err != nil {
		return err
	}

	e.refreshSitemapCache()

	log.Info().Msgf("[load] all systems ready in %s (%d companies indexed)", time.Since(start).Round(time.Millisecond), len(companies))
	return nil
}

func buildBleveMapping() (*mapping.IndexMappingImpl, error) {
	m := bleve.NewIndexMapping()
	docMap := bleve.NewDocumentMapping()

	stdText := bleve.NewTextFieldMapping()
	stdText.Analyzer = "standard"

	keyword := bleve.NewKeywordFieldMapping()
	keyword.Analyzer = "keyword"
	geo := bleve.NewGeoPointFieldMapping()

	docMap.AddFieldMappingsAt(fieldName, stdText)
	docMap.AddFieldMappingsAt(fieldActivity, stdText)
	docMap.AddFieldMappingsAt(fieldAddress, stdText)
	docMap.AddFieldMappingsAt(fieldAddressLine2, stdText)
	docMap.AddFieldMappingsAt(fieldCity, keyword)
	docMap.AddFieldMappingsAt(fieldCountry, keyword)
	docMap.AddFieldMappingsAt(fieldDescription, stdText)
	docMap.AddFieldMappingsAt(fieldSlogan, stdText)
	docMap.AddFieldMappingsAt(fieldLocation, geo)

	m.DefaultMapping = docMap
	m.DefaultAnalyzer = "standard"
	return m, nil
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
			fieldCity:         normalizeForSearch(c.City),
			fieldCountry:      normalizeForSearch(c.Country),
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

func (e *Engine) indexSem(chromemPath string) error {
	db, err := chromem.NewPersistentDB(chromemPath, false)
	if err != nil {
		return err
	}

	embFn := chromem.EmbeddingFunc(func(ctx context.Context, text string) ([]float32, error) {
		return e.embed(ctx, text)
	})

	modelMarker := chromemPath + ".model"
	needsRebuild := false

	if stored, err := os.ReadFile(modelMarker); err != nil {
		needsRebuild = true
	} else if strings.TrimSpace(string(stored)) != e.Config.EmbeddingModel {
		log.Warn().Msgf("[load] embedding model changed (%s → %s), rebuilding semantic index...",
			strings.TrimSpace(string(stored)), e.Config.EmbeddingModel)
		needsRebuild = true
	}

	coll := db.GetCollection("companies", embFn)

	if coll != nil && needsRebuild {
		if err := os.RemoveAll(chromemPath); err != nil {
			return fmt.Errorf("remove stale chromem index: %w", err)
		}

		db, err = chromem.NewPersistentDB(chromemPath, false)
		if err != nil {
			return err
		}
		coll = nil
	}

	if coll == nil {
		log.Info().Msg("[load] building Chromem semantic index from scratch...")

		coll, err = db.CreateCollection("companies", map[string]string{}, embFn)
		if err != nil {
			return err
		}
		e.chromemColl = coll

		indexCtx, indexCancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer indexCancel()

		total := len(e.companies)
		for start := 0; start < total; start += chromemBatch {
			end := start + chromemBatch
			if end > total {
				end = total
			}

			docs := make([]chromem.Document, 0, end-start)
			for _, c := range e.companies[start:end] {
				text := strings.Join([]string{
					c.Name, c.Activity, c.Slogan,
					c.Address, c.AddressLine2,
					c.City, c.Country, c.Description,
				}, " ")

				if textRunes := []rune(text); len(textRunes) > 1800 {
					text = string(textRunes[:1800])
				}

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

			if err := coll.AddDocuments(indexCtx, docs, 4); err != nil {
				return err
			}

			log.Info().Msgf("[load] chromem indexed %d/%d companies",
				min(start+chromemBatch, total), total)
		}

		if err := os.WriteFile(modelMarker, []byte(e.Config.EmbeddingModel), 0644); err != nil {
			log.Warn().Msgf("[load] could not write model marker: %v", err)
		}
	} else {
		log.Info().Msgf("[load] loading existing Chromem index (model: %s)",
			e.Config.EmbeddingModel)
		e.chromemColl = coll
	}

	return nil
}

func (e *Engine) pingOllama() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url := strings.TrimSuffix(e.Config.OllamaURL, "/") + "/api/tags"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	resp, err := e.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama ping returned %s", resp.Status)
	}
	return nil
}

func (e *Engine) embed(ctx context.Context, text string) ([]float32, error) {
	body, _ := json.Marshal(ollamaEmbedRequest{
		Model:  e.Config.EmbeddingModel,
		Prompt: text,
	})

	embedCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(embedCtx, "POST",
		strings.TrimSuffix(e.Config.OllamaURL, "/")+"/api/embeddings",
		bytes.NewBuffer(body),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama embed: %w", err)
	}
	defer resp.Body.Close()

	var out ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	return out.Embedding, nil
}

func (e *Engine) HybridSearch(q string) ([]SearchResult, error) {
	e.mu.RLock()
	bleveIdx := e.bleveIdx
	chromemColl := e.chromemColl
	knownCities := e.knownCities
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
		h, err := e.runBleveSearch(q, knownCities)
		bleveCh <- result{h, err}
	}()
	go func() {
		h, err := e.runChromemSearch(q)
		semCh <- result{h, err}
	}()

	br := <-bleveCh
	if br.err != nil {
		return nil, fmt.Errorf("bleve: %w", br.err)
	}

	sr := <-semCh
	if sr.err != nil {
		log.Warn().Err(sr.err).Msg("[search] semantic fallback to bleve-only")
		sr.hits = map[string]float64{}
	}

	merged := reciprocalRankFusion(
		hitsToRanking(br.hits),
		hitsToRanking(sr.hits),
	)

	geoTokens, _ := extractGeoTokens(q, knownCities)
	if len(geoTokens) > 0 {
		for id := range merged {
			if _, ok := br.hits[id]; !ok {
				delete(merged, id)
			}
		}
	}

	type idScore struct {
		id    string
		score float64
	}
	ranked := make([]idScore, 0, len(merged))
	for id, s := range merged {
		ranked = append(ranked, idScore{id, s})
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].score > ranked[j].score
	})

	if len(ranked) > 0 {
		maxScore := ranked[0].score
		for i := range ranked {
			ranked[i].score = ranked[i].score / maxScore
		}
	}

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

func (e *Engine) runBleveSearch(q string, knownCities map[string]bool) (map[string]float64, error) {
	geoTokens, activityQ := extractGeoTokens(q, knownCities)

	topQ := bleve.NewBooleanQuery()

	if activityQ != "" {
		actQ := bleve.NewBooleanQuery()

		mq := bleve.NewMatchQuery(activityQ)
		mq.SetField(fieldName)
		mq.SetBoost(3.0)
		actQ.AddShould(mq)

		aq := bleve.NewMatchQuery(activityQ)
		aq.SetField(fieldActivity)
		aq.SetBoost(3.0)
		actQ.AddShould(aq)

		dq := bleve.NewMatchQuery(activityQ)
		dq.SetField(fieldDescription)
		dq.SetBoost(1.0)
		actQ.AddShould(dq)

		actQ.SetMinShould(1)
		topQ.AddMust(actQ)
	}

	for _, tok := range geoTokens {
		geoQ := bleve.NewBooleanQuery()

		cq := bleve.NewTermQuery(tok)
		cq.SetField(fieldCity)
		geoQ.AddShould(cq)

		coq := bleve.NewTermQuery(tok)
		coq.SetField(fieldCountry)
		geoQ.AddShould(coq)

		geoQ.SetMinShould(1)
		topQ.AddMust(geoQ)
	}

	if activityQ == "" && len(geoTokens) == 0 {
		return map[string]float64{}, nil
	}

	req := bleve.NewSearchRequestOptions(topQ, MaxResults*2, 0, false)
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

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

func (e *Engine) GenerateAnswer(userQuery string, topResults []SearchResult) (string, error) {
	if len(topResults) == 0 {
		return "Désolé, je n'ai trouvé aucune entreprise pertinente pour répondre à votre question.", nil
	}

	limit := 15
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
		Model:  e.Config.AiModel,
		Prompt: sb.String(),
		Stream: false,
		Options: map[string]interface{}{
			"num_predict": 150,
			"temperature": 0.2,
			"num_thread":  2,
		},
	})

	ollamaGenerateURL := strings.TrimSuffix(e.Config.OllamaURL, "/") + "/api/generate"

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", ollamaGenerateURL, bytes.NewBuffer(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("Ollama connection: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read Ollama response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Ollama returned %s: %s", resp.Status, string(respBody))
	}

	var out ollamaResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", fmt.Errorf("decode Ollama response: %w", err)
	}
	return out.Response, nil
}

func (e *Engine) generateSitemapEntries() []SitemapEntry {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entries := make([]SitemapEntry, 0, len(e.companies)+4)

	staticPages := []struct {
		path       string
		changefreq string
		priority   float64
	}{
		{"/", "daily", 1.0},
		{"/help", "weekly", 0.5},
		{"/privacy", "monthly", 0.3},
		{"/terms", "monthly", 0.3},
	}
	now := time.Now()
	for _, p := range staticPages {
		entries = append(entries, SitemapEntry{
			Loc:        p.path,
			LastMod:    now,
			ChangeFreq: p.changefreq,
			Priority:   p.priority,
		})
	}

	for _, comp := range e.companies {
		if comp.NameSeo == "" {
			continue
		}

		lastMod := comp.UpdatedAt
		if lastMod.IsZero() {
			lastMod = now
		}
		entries = append(entries, SitemapEntry{
			Loc:        "/company/" + comp.NameSeo,
			LastMod:    lastMod,
			ChangeFreq: "monthly",
			Priority:   0.6,
		})
	}
	return entries
}

func (e *Engine) refreshSitemapCache() {
	entries := e.generateSitemapEntries()
	var buf bytes.Buffer
	if err := e.WriteSitemapXML(&buf, entries); err != nil {
		log.Error().Msgf("[sitemap] generation error: %v", err)
		return
	}
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	_, _ = gz.Write(buf.Bytes())
	gz.Close()

	e.SitemapMu.Lock()
	e.SitemapCache = gzBuf.Bytes()
	e.SitemapMu.Unlock()
}

func (e *Engine) WriteSitemapXML(w io.Writer, entries []SitemapEntry) error {
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>` + "\n"))
	w.Write([]byte(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n"))

	for _, e := range entries {
		fmt.Fprintf(w, "  <url>\n")
		fmt.Fprintf(w, "    <loc>https://congopro.com%s</loc>\n", e.Loc)
		fmt.Fprintf(w, "    <lastmod>%s</lastmod>\n", e.LastMod.Format("2006-01-02"))
		fmt.Fprintf(w, "    <changefreq>%s</changefreq>\n", e.ChangeFreq)
		fmt.Fprintf(w, "    <priority>%.1f</priority>\n", e.Priority)
		fmt.Fprintf(w, "  </url>\n")
	}
	w.Write([]byte(`</urlset>`))
	return nil
}
