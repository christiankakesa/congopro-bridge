package templates

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The .content-page defaults style trusted admin-authored HTML from
// content/*.html, which also carries Tailwind utility classes. Those
// defaults MUST live in @layer components so the utilities (in the later
// @layer utilities) win.
//
// This is a layer question, not a specificity one: UNLAYERED css beats
// every layered rule regardless of how weak its selector is. That is how
// `.content-page a { color: var(--accent) }` painted the contact button's
// label accent-red on an accent-red background, and why rewriting it as
// zero-specificity `:where()` did not fix it — only moving it into a layer
// did.
// Browsers render <button> with an arrow cursor, which makes buttons read as
// non-interactive beside real links. One base rule covers every button, so
// new ones inherit it — 19 were missing the hand when this was added.
func TestButtonsGetPointerCursor(t *testing.T) {
	css, err := os.ReadFile("../css/input.css")
	if err != nil {
		t.Fatal(err)
	}
	src := string(css)
	if !strings.Contains(src, "button:not(:disabled)") {
		t.Error("no base rule giving buttons cursor:pointer")
	}
	if !strings.Contains(src, "button:disabled") || !strings.Contains(src, "not-allowed") {
		t.Error("disabled buttons should show not-allowed, not the hand")
	}
	// it must sit in @layer base so .btn-busy's cursor:progress still wins
	i := strings.Index(src, "button:not(:disabled)")
	if j := strings.LastIndex(src[:i], "@layer base {"); j < 0 {
		t.Error("the cursor rule must live in @layer base, or component/utility cursors cannot override it")
	}
}

func TestContentPageDefaultsAreLayered(t *testing.T) {
	css, err := os.ReadFile("../css/input.css")
	if err != nil {
		t.Fatal(err)
	}
	src := string(css)

	i := strings.Index(src, ".content-page")
	if i < 0 {
		t.Fatal(".content-page block not found")
	}
	// walk back to the nearest enclosing at-rule/brace context
	head := src[:i]
	lastLayer := strings.LastIndex(head, "@layer components {")
	lastClose := strings.LastIndex(head, "\n}")
	if lastLayer < 0 || lastLayer < lastClose {
		t.Error(".content-page rules are unlayered — wrap them in @layer components, " +
			"or utilities used inside content/*.html cannot override them")
	}
	if bad := regexp.MustCompile(`(?m)^\.content-page\s+[a-z]`).FindString(src); bad != "" {
		t.Errorf("high-specificity content rule %q — prefer :where(.content-page) :where(el)", strings.TrimSpace(bad))
	}
}
