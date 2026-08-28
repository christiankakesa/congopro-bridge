package templates

import (
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var (
	phoneRe    = regexp.MustCompile(`^[+\d\s\-().]+$`)
	emailRe    = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	cssColorRe = regexp.MustCompile(`(?i)^(#[0-9a-f]{3,8}|rgba?\(\s*\d+\s*,\s*\d+\s*,\s*\d+(\s*,\s*[\d.]+)?\s*\)|hsla?\(\s*\d+\s*,\s*\d+%\s*,\s*\d+%(\s*,\s*[\d.]+)?\s*\)|[a-z]+)$`)
	ipv4Re     = regexp.MustCompile(`^(\d{1,3}\.){3}\d{1,3}$`)
	priv172    = regexp.MustCompile(`^172\.(1[6-9]|2\d|3[01])\.`)
	nonDigit   = regexp.MustCompile(`[^0-9+]`)
	wordRe     = regexp.MustCompile(`[\p{L}\p{N}]+`)
)

// ValidateURL is a Go port of the frontend's validateURL(): only absolute
// https URLs with a public-looking hostname are accepted. Returns "" if the
// input is empty or fails validation.
func ValidateURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || host == "." || !strings.Contains(host, ".") {
		return ""
	}
	if host == "localhost" ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".internal") ||
		strings.HasPrefix(host, "127.") ||
		strings.HasPrefix(host, "10.") ||
		strings.HasPrefix(host, "192.168.") ||
		priv172.MatchString(host) ||
		ipv4Re.MatchString(host) {
		return ""
	}
	return u.String()
}

// SafePhone mirrors the frontend's safePhone(): digits, spaces and the usual
// phone punctuation only.
func SafePhone(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" && phoneRe.MatchString(raw) {
		return raw
	}
	return ""
}

// SafeEmail mirrors the frontend's safeEmail(): a minimal shape check, not
// full RFC 5322 validation — same intentionally loose rule as the client.
func SafeEmail(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" && emailRe.MatchString(raw) {
		return raw
	}
	return ""
}

// Initials is a Go port of the frontend's initials(): strip diacritics, pick
// the first letter of the first two "real" (3+ char) words, uppercasing only
// the second — matching the client's exact (slightly asymmetric) behavior.
func Initials(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "?"
	}
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	normalized, _, err := transform.String(t, name)
	if err != nil {
		normalized = name
	}
	words := wordRe.FindAllString(normalized, -1)
	if len(words) == 0 {
		words = []string{"?"}
	}
	if len(words) == 1 {
		r := []rune(words[0])
		if len(r) > 2 {
			r = r[:2]
		}
		return strings.ToUpper(string(r))
	}

	var filtered []string
	for _, w := range words {
		if utf8.RuneCountInString(w) >= 3 {
			filtered = append(filtered, w)
		}
	}

	first := firstRune(words[0])
	second := firstRune(words[1])
	if len(filtered) > 0 {
		first = firstRune(filtered[0])
	}
	if len(filtered) > 1 {
		second = firstRune(filtered[1])
	}
	return first + strings.ToUpper(second)
}

func firstRune(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return ""
	}
	return string(r[0])
}

// NameToHue is a Go port of the frontend's nameToHue() deterministic
// name-to-color hash, used for the avatar background.
func NameToHue(name string) int {
	h := 0
	for _, r := range name {
		h = (h*31 + int(r)) & 0xffff
	}
	return h % 360
}

// StreetAddressHTML mirrors the frontend's streetHtml: a single
// itemprop="streetAddress" span with the two address lines joined by <br/>,
// each HTML-escaped individually.
func StreetAddressHTML(address1, address2 string) string {
	parts := make([]string, 0, 2)
	if address1 != "" {
		parts = append(parts, html.EscapeString(address1))
	}
	if address2 != "" {
		parts = append(parts, html.EscapeString(address2))
	}
	if len(parts) == 0 {
		return ""
	}
	return `<span itemprop="streetAddress" class="block">` + strings.Join(parts, "<br/>") + `</span>`
}

// CityCountryHTML mirrors the frontend's locationHtml: city/country spans
// joined by a plain ", " separator with no stray whitespace, each
// HTML-escaped individually.
func CityCountryHTML(city, country string) string {
	parts := make([]string, 0, 2)
	if city != "" {
		parts = append(parts, `<span itemprop="addressLocality">`+html.EscapeString(city)+`</span>`)
	}
	if country != "" {
		parts = append(parts, `<span itemprop="addressCountry">`+html.EscapeString(country)+`</span>`)
	}
	if len(parts) == 0 {
		return ""
	}
	return `<span class="text-ink-faint block mt-0.5">` + strings.Join(parts, ", ") + `</span>`
}

// DomainFromURL extracts a display-friendly hostname (no "www.") from an
// already-validated https URL, mirroring the frontend's inline logic.
func DomainFromURL(safeURL string) string {
	if safeURL == "" {
		return ""
	}
	u, err := url.Parse(safeURL)
	if err != nil {
		return ""
	}
	return strings.Replace(u.Hostname(), "www.", "", 1)
}

// MapsURL mirrors the frontend's mapUrl logic: prefer the free-text address,
// fall back to lat/lon, otherwise empty (no maps link rendered).
func MapsURL(address1, address2, city, country string, lat, lon float64, hasLocation bool) string {
	if address1 != "" {
		parts := make([]string, 0, 4)
		for _, p := range []string{address1, address2, city, country} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		return "https://google.com/maps/search/?api=1&query=" + encodeURIComponent(strings.Join(parts, ", "))
	}
	if hasLocation && lat != 0 && lon != 0 {
		return fmt.Sprintf("https://google.com/maps/search/?api=1&query=%v,%v", lat, lon)
	}
	return ""
}

// WhatsAppURL mirrors the frontend's waTel logic: prefer an explicit
// WhatsApp URL, fall back to the phone number stripped to digits/+.
func WhatsAppURL(safeWhatsapp, safeTel string) string {
	waTel := safeWhatsapp
	if waTel == "" && safeTel != "" {
		waTel = nonDigit.ReplaceAllString(safeTel, "")
	}
	if waTel == "" {
		return ""
	}
	if strings.HasPrefix(waTel, "http") {
		return waTel
	}
	return "https://wa.me/" + waTel
}

// VCardURI builds a data: URI for a downloadable vCard, mirroring the
// frontend's generateVCardURI() byte for byte — including its use of HTML
// escaping (not vCard escaping) on field values, preserved here for parity.
func VCardURI(name, phone, email, website, address, city, country string) string {
	if name == "" {
		name = "Contact"
	}
	var sb strings.Builder
	sb.WriteString("BEGIN:VCARD\nVERSION:3.0\nFN:")
	sb.WriteString(html.EscapeString(name))
	sb.WriteString("\nORG:")
	sb.WriteString(html.EscapeString(name))
	sb.WriteString("\n")
	if phone != "" {
		sb.WriteString("TEL;TYPE=WORK,VOICE:" + html.EscapeString(phone) + "\n")
	}
	if email != "" {
		sb.WriteString("EMAIL;TYPE=PREF,INTERNET:" + html.EscapeString(email) + "\n")
	}
	if website != "" {
		sb.WriteString("URL:" + html.EscapeString(website) + "\n")
	}
	adrParts := make([]string, 0, 3)
	for _, p := range []string{address, city, country} {
		if p != "" {
			adrParts = append(adrParts, p)
		}
	}
	if len(adrParts) > 0 {
		sb.WriteString("ADR;TYPE=WORK:;;" + html.EscapeString(strings.Join(adrParts, ", ")) + "\n")
	}
	sb.WriteString("END:VCARD")

	return "data:text/vcard;charset=utf-8," + encodeURIComponent(sb.String())
}

// encodeURIComponent mirrors JavaScript's encodeURIComponent byte for byte:
// unreserved characters pass through, everything else (including spaces,
// which become %20, not +) is percent-encoded.
func encodeURIComponent(s string) string {
	var sb strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if isUnreservedURIComponent(b) {
			sb.WriteByte(b)
		} else {
			fmt.Fprintf(&sb, "%%%02X", b)
		}
	}
	return sb.String()
}

func isUnreservedURIComponent(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '-', '_', '.', '!', '~', '*', '\'', '(', ')':
		return true
	}
	return false
}

// Excerpt is a Go port of the frontend's excerpt(): truncate to at most max
// runes, breaking at the last plain space at or before that position. If no
// space is found, the frontend's quirk of returning the full, untruncated
// text is preserved here too.
func Excerpt(text string, max int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	cut := -1
	for i := max; i >= 0; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	if cut == -1 {
		return text
	}
	return strings.TrimRightFunc(string(runes[:cut]), unicode.IsSpace) + "…"
}

// TruncateActivity is a Go port of the frontend's truncateActivity() (fixed
// 32-rune limit, the only value it's ever called with): unlike Excerpt, a
// missing space falls back to a hard cut at max runes rather than returning
// the untruncated text.
func TruncateActivity(activity string) string {
	const max = 32
	activity = strings.TrimSpace(activity)
	runes := []rune(activity)
	if len(runes) <= max {
		return activity
	}
	cut := -1
	for i := max; i >= 0; i-- {
		if runes[i] == ' ' {
			cut = i
			break
		}
	}
	if cut > 0 {
		return strings.TrimRightFunc(string(runes[:cut]), unicode.IsSpace) + "…"
	}
	return strings.TrimRightFunc(string(runes[:max]), unicode.IsSpace) + "…"
}

// MapsURLForRow mirrors the frontend's renderRow() mapsLink logic, which —
// unlike the company-profile page's MapsURL — prefers GPS coordinates over
// the free-text address when both are present.
func MapsURLForRow(address1, address2, city, country string, lat, lon float64, hasLocation bool) string {
	var raw string
	if hasLocation && lat != 0 && lon != 0 {
		raw = fmt.Sprintf("https://google.com/maps/search/?api=1&query=%v,%v", lat, lon)
	} else {
		parts := make([]string, 0, 4)
		for _, p := range []string{address1, address2, city, country} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		if len(parts) > 0 {
			raw = "https://google.com/maps/search/?api=1&query=" + encodeURIComponent(strings.Join(parts, ", "))
		}
	}
	if raw == "" {
		return ""
	}
	return ValidateURL(raw)
}

// LocationPillAddressHTML renders the inline address spans inside the
// visible location pill (distinct from the hidden AddressMetaHTML below,
// which always renders regardless of whether the pill itself does).
//
// Country is deliberately NOT shown: the site is DRC-only, and the raw value
// ("Democratic Republic of the Congo") made every pill long. It stays in the
// hidden AddressMetaHTML block, so schema.org output is unaffected.
//
// The parts are joined with ", " OUTSIDE the spans: inline spans render with
// no whitespace of their own, so without the separator the visible text ran
// together ("GombeKinshasa"), while keeping it outside each span keeps the
// itemprop values clean for microdata parsers.
func LocationPillAddressHTML(address1, address2, city string) string {
	var sb strings.Builder
	first := true
	for _, p := range []struct{ prop, val string }{
		{"streetAddress", address1},
		{"streetAddress", address2},
		{"addressLocality", city},
	} {
		if p.val == "" {
			continue
		}
		if !first {
			sb.WriteString(", ")
		}
		sb.WriteString(`<span itemprop="` + p.prop + `">` + html.EscapeString(p.val) + `</span>`)
		first = false
	}
	return sb.String()
}

// GeoMetaHTML mirrors renderRow()'s hidden, machine-readable geo block —
// rendered whenever coordinates exist, independent of whether the visible
// location pill renders.
func GeoMetaHTML(lat, lon float64, hasLocation bool) string {
	if !hasLocation || lat == 0 || lon == 0 {
		return ""
	}
	return `<div itemprop="geo" itemscope itemtype="https://schema.org/GeoCoordinates" hidden>` +
		`<meta itemprop="latitude" content="` + html.EscapeString(fmt.Sprintf("%v", lat)) + `"/>` +
		`<meta itemprop="longitude" content="` + html.EscapeString(fmt.Sprintf("%v", lon)) + `"/>` +
		`</div>`
}

// AddressMetaHTML mirrors renderRow()'s hidden, machine-readable address
// block — rendered whenever any address field exists, independent of
// whether the visible location pill renders.
func AddressMetaHTML(address1, address2, city, country string) string {
	if address1 == "" && address2 == "" && city == "" && country == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<div itemprop="address" itemscope itemtype="https://schema.org/PostalAddress" hidden>`)
	for _, p := range []struct{ prop, val string }{
		{"streetAddress", address1},
		{"streetAddress", address2},
		{"addressLocality", city},
		{"addressCountry", country},
	} {
		if p.val == "" {
			continue
		}
		sb.WriteString(`<meta itemprop="` + p.prop + `" content="` + html.EscapeString(p.val) + `"/>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// SafeCSSColor is a Go port of the frontend's safeCSSColor(): allow hex,
// rgb()/hsl(), or a bare named color; reject anything else (in particular,
// anything that could break out of a style="" attribute).
func SafeCSSColor(val string) string {
	val = strings.TrimSpace(val)
	if val == "" || !cssColorRe.MatchString(val) {
		return ""
	}
	return val
}

// AdCTATextColor picks white or near-black text for an opaque, admin-chosen
// ad background color — whichever meets WCAG AA (4.5:1). Advertisers pick
// arbitrary hex colors, and mid-tones like #ea8600 fail with the white text
// the CTA used to hardcode (flagged by Lighthouse at 2.65:1). Whenever white
// fails, near-black passes, so the pair always yields a compliant choice.
// Non-hex colors (rgb()/hsl()/named, rare in practice) keep white.
func AdCTATextColor(hexColor string) string {
	r, g, b, ok := parseHexRGB(hexColor)
	if !ok {
		return "#ffffff"
	}
	lum := relativeLuminance(r, g, b)
	if 1.05/(lum+0.05) >= 4.5 {
		return "#ffffff"
	}
	return "#111827"
}

func parseHexRGB(s string) (r, g, b uint8, ok bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "#") {
		return 0, 0, 0, false
	}
	s = s[1:]
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v), true
}

// relativeLuminance implements the WCAG 2.x formula.
func relativeLuminance(r, g, b uint8) float64 {
	lin := func(c uint8) float64 {
		v := float64(c) / 255
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}
