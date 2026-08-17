package data

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLooksLikeRefusal(t *testing.T) {
	tests := []struct {
		name string
		ans  string
		want bool
	}{
		{"mandated refusal", "Je l'ignore.", true},
		{"refusal with apology", "Désolé, je l'ignore.", true},
		{"refusal padded", "  Je l'ignore.  ", true},
		{"accented trouve", "Je n'ai pas trouvé cette information.", true},
		{"cannot answer", "Je ne peux pas répondre à cette question.", true},
		{"introuvable", "Introuvable dans le contexte fourni.", true},
		{"english", "I don't know.", true},
		{"empty output", "", true},
		{"whitespace only", "   ", true},
		{"grounded answer", "Oui, TOTAL Énergies vend du carburant à Kinshasa, avenue de la Justice.", false},
		{"hedge but useful", "Je ne peux pas confirmer le prix, mais trois pharmacies à Goma livrent la quinine : A, B et C.", false},
		{"refusal phrase in long answer", "Il n'y a pas de restaurant ougandais dans nos données, introuvable en RDC via ce terme exact, mais voici des restaurants similaires à Kinshasa : Da Giovanni (italien, Gombe), Chez Wenge (mixte, Limete) et Mama Ntemba (congolais, Bandal).", false},
		{"short unrelated", "Trois entreprises correspondent.", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := looksLikeRefusal(tt.ans); got != tt.want {
				t.Fatalf("looksLikeRefusal(%q) = %v, want %v", tt.ans, got, tt.want)
			}
		})
	}
}

func TestBuildFallbackInsight(t *testing.T) {
	results := []SearchResult{
		{Company: Company{Name: "A", City: "Kinshasa", Activity: "Restauration"}},
		{Company: Company{Name: "B", City: "Kinshasa", Activity: "Restauration"}},
		{Company: Company{Name: "C", City: "Kinshasa", Activity: "BTP"}},
		{Company: Company{Name: "D", City: "Goma", Activity: "BTP"}},
		{Company: Company{Name: "E"}}, // empty city + activity: must be skipped
	}

	got := buildFallbackInsight("prix du ciment", results)

	for _, want := range []string{
		"« prix du ciment »",
		"5 entreprises",
		"Kinshasa (3)",
		"Goma (1)",
		"Restauration (2)",
		"BTP (2)",
		"précisez votre recherche",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("fallback missing %q in:\n%s", want, got)
		}
	}
}

func TestBuildFallbackInsight_Singular(t *testing.T) {
	got := buildFallbackInsight("q", []SearchResult{
		{Company: Company{Name: "A", City: "Kinshasa"}},
	})
	if !strings.Contains(got, "1 entreprise") || strings.Contains(got, "1 entreprises") {
		t.Fatalf("expected singular form in:\n%s", got)
	}
	if !strings.Contains(got, "Kinshasa (1)") {
		t.Errorf("expected city stat in:\n%s", got)
	}
}

func TestTopCounted_DeterministicTieBreak(t *testing.T) {
	counts := map[string]int{"Zongo": 2, "Boma": 2, "Kinshasa": 5}
	got := topCounted(counts, 3)
	want := []string{"Kinshasa (5)", "Boma (2)", "Zongo (2)"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v (tie-break must be alphabetical)", got, want)
		}
	}
}

func TestTopCounted_LimitsAndEmpty(t *testing.T) {
	if got := topCounted(map[string]int{}, 3); got != nil {
		t.Fatalf("expected nil for empty map, got %v", got)
	}
	counts := map[string]int{"A": 3, "B": 2, "C": 1, "D": 1}
	if got := topCounted(counts, 2); len(got) != 2 || got[0] != "A (3)" || got[1] != "B (2)" {
		t.Fatalf("expected top 2 [A (3) B (2)], got %v", got)
	}
}

// Zero results return before any Ollama I/O, so this runs without a model.
func TestGenerateAnswer_NoResults(t *testing.T) {
	e := &Engine{}
	got, err := e.GenerateAnswer("importateur de riz", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "« importateur de riz »") {
		t.Errorf("expected query echoed in no-results message, got:\n%s", got)
	}
}

func TestEmbedderSettings(t *testing.T) {
	raw := embedderSettings("http://ollama:11434/", "nomic-embed-text")

	// It is sent as a JSON body — it must parse.
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("embedder settings are not valid JSON: %v\n%s", err, raw)
	}
	for _, want := range []string{
		"http://ollama:11434/api/embeddings", // trailing slash trimmed
		"nomic-embed-text",
		"search_document",
	} {
		if !strings.Contains(raw, want) {
			t.Errorf("embedder settings missing %q in:\n%s", want, raw)
		}
	}
}
