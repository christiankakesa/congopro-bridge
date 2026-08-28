package main

import (
	"strings"
	"testing"
)

func TestFormatDigest(t *testing.T) {
	got := formatDigest(digestData{
		Date:             "27/08/2026",
		CompaniesAdded:   3,
		PendingClaims:    2,
		ActivePromotions: 1,
		MRR:              "15.00 $ / mois",
	})
	want := "📊 Congopro — bilan du 27/08/2026\n" +
		"Entreprises ajoutées : 3\n" +
		"Réclamations en attente : 2\n" +
		"Mises en avant actives : 1\n" +
		"MRR : 15.00 $ / mois"
	if got != want {
		t.Errorf("formatDigest = %q, want %q", got, want)
	}
}

// Stripe being unreadable degrades the MRR line — never the digest.
func TestFormatDigest_MRRUnavailable(t *testing.T) {
	got := formatDigest(digestData{Date: "27/08/2026"})
	if want := "MRR : indisponible"; !strings.Contains(got, want) {
		t.Errorf("digest %q missing %q", got, want)
	}
}
