package templates

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"congopro-bridge/internal/claims"
	"congopro-bridge/internal/data"
)

func render(t *testing.T, c interface {
	Render(context.Context, io.Writer) error
}) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(context.Background(), &b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// htmx implements hx-confirm with window.confirm(), which ignores the theme
// and prefixes the message with the hostname. The styled dialog must be
// present wherever hx-confirm is used, and hooked to htmx:confirm.
func TestAdminConfirmDialogIsStyled(t *testing.T) {
	nav := AdminNav{UserName: "CK", Active: "claims"}
	h := render(t, AdminClaimsList("N", nav, "pending", []claims.Claim{
		{ID: "c1", CompanyName: "Congopro", Status: "pending", CreatedAt: time.Now()},
	}))
	for _, want := range []string{
		`id="confirmDialog"`, `class="confirm-dialog"`,
		`htmx:confirm`, `showModal`,
		`hx-confirm="Approuver cette réclamation ?"`,
		`data-confirm-variant="danger"`, // reject is styled destructive
	} {
		if !strings.Contains(h, want) {
			t.Errorf("claims page missing %q", want)
		}
	}
	if !strings.Contains(h, "cancel.focus()") {
		t.Error("cancel should take initial focus — these actions email a real person")
	}
}

// The share button's clipboard path used to alert(). No native dialog should
// remain in shipped markup or scripts.
func TestNoNativeDialogsInSharedSurfaces(t *testing.T) {
	c := data.Company{Name: "Congopro", NameSeo: "congopro"}
	pages := map[string]string{
		"home":    render(t, HomePage("", "https://x/", "N", CSSVersion, 100)),
		"profile": render(t, CompanyPage("C", "https://x/", "N", CSSVersion, &c, false, false)),
	}
	for name, h := range pages {
		if strings.Contains(h, "alert(") {
			t.Errorf("%s: native alert() still present", name)
		}
		if !strings.Contains(h, "Lien copié") {
			t.Errorf("%s: expected inline copied feedback", name)
		}
		if !strings.Contains(h, "js-share-btn") {
			t.Errorf("%s: share handler missing", name)
		}
	}
}
