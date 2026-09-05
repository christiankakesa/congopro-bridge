package templates

import (
	"context"
	"strings"
	"testing"
)

func renderToString(t *testing.T, render func(w *strings.Builder) error) string {
	t.Helper()
	var sb strings.Builder
	if err := render(&sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

func TestAdminAdPreviewUsesRealComponents(t *testing.T) {
	form := AdminAdFormData{
		Title: "Rawbank", Description: "Banque", URL: "https://rawbank.cd",
		DisplayURL: "rawbank.cd", Label: "Premium", Color: "#5e35b1", Placement: "search_results",
	}
	out := renderToString(t, func(w *strings.Builder) error {
		return AdminAdPreview(form).Render(context.Background(), w)
	})
	for _, want := range []string{`class="ad-slot"`, "Rawbank", "#5e35b1", "Premium", `href="https://rawbank.cd"`} {
		if !strings.Contains(out, want) {
			t.Errorf("search preview missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "URL de destination manquante") {
		t.Errorf("valid URL should not show the missing-URL note")
	}

	form.Placement = "homepage"
	out = renderToString(t, func(w *strings.Builder) error {
		return AdminAdPreview(form).Render(context.Background(), w)
	})
	if !strings.Contains(out, `data-ad-placement="homepage"`) || !strings.Contains(out, "Découvrir") {
		t.Errorf("homepage preview should use premiumHomepageAd\n%s", out)
	}
}

func TestAdminAdPreviewNeverBlank(t *testing.T) {
	out := renderToString(t, func(w *strings.Builder) error {
		return AdminAdPreview(AdminAdFormData{Placement: "search_results"}).Render(context.Background(), w)
	})
	for _, want := range []string{`class="ad-slot"`, "Titre de la campagne", "exemple.cd", "URL de destination manquante"} {
		if !strings.Contains(out, want) {
			t.Errorf("empty-form preview missing %q\n%s", want, out)
		}
	}
	// Placeholder title must not leak a click through to javascript: etc.
	out = renderToString(t, func(w *strings.Builder) error {
		return AdminAdPreview(AdminAdFormData{URL: "javascript:alert(1)"}).Render(context.Background(), w)
	})
	if strings.Contains(out, "javascript:") {
		t.Errorf("unsafe URL leaked into preview\n%s", out)
	}
}

func TestAdminAdFormCarriesPresetColorsAndPreview(t *testing.T) {
	presets := []AdLabelPreset{{Label: "Premium", Color: "#5e35b1"}}
	out := renderToString(t, func(w *strings.Builder) error {
		return AdminAdForm("n0nce", AdminNav{UserName: "x"}, AdminAdFormData{Placement: "homepage"}, true, "", nil, presets).Render(context.Background(), w)
	})
	for _, want := range []string{
		`<option value="Premium" data-color="#5e35b1">`,
		`href="/ads-preview"`,
		`hx-post="/admin/ads/preview"`,
		`id="ad-preview"`,
		`<script nonce="n0nce">`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("form missing %q", want)
		}
	}
}
