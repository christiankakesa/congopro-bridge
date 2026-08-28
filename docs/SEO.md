# SEO & PageSpeed

State of play after the 2026-08-29 milestone: what the code now guarantees,
and the account-side steps (Search Console, Analytics) that only a human with
the Google account can do.

## The one rule: the canonical host is `congopro.com` (no www)

Everything now agrees on the apex domain — canonical tags, og/twitter URLs,
sitemap, robots.txt, and a Traefik 301 from `www.congopro.com` to
`congopro.com`. Before this milestone the canonical tag said `www` while the
sitemap said apex and **both hosts answered 200**, which fed Google two
competing copies of every page. Never reintroduce a `www` URL in a template
or config; `TestLayoutCanonicalHost` pins this.

## What the code does now (2026-08-29 milestone)

* **www → apex 301** — `congopro-bridge-redirect-www` middleware in
  [deploy/traefik/dynamic/congopro-bridge.yml](../deploy/traefik/dynamic/congopro-bridge.yml)
  (deployed via `make prod-config-push`, included in `make prod-deploy`).
* **Per-page meta** — `Layout` takes a `description` param; og:url/og:title
  and the twitter tags follow the page's canonical URL and title instead of
  being hardcoded to the homepage. Company profiles compose their snippet
  from the company's own description (or name — activity à city), capped at
  ~160 chars (`companyMetaDescription`).
* **Structured data** — homepage emits JSON-LD `WebSite` (with
  `SearchAction` for a sitelinks search box, target `/?q={search_term_string}`)
  and `Organization`. Company pages keep their schema.org microdata
  (`LocalBusiness` in `companyProfileCard`).
* **Sitemap** — `/sitemap.xml` (plain XML, Traefik compresses on the wire)
  is the URL robots.txt advertises and the one to submit in Search Console.
  `/sitemap.xml.gz` still exists for crawlers that learned it from the old
  robots.txt and now serves a *real* gzip file (`application/gzip`; the old
  handler used `Content-Encoding: gzip`, so the wire delivered plain XML
  under a .gz name).
* **robots.txt** — dropped `Crawl-delay: 5` (Google ignores it; Bing obeys it
  and it throttled crawl of ~1500 company pages), cache cut from
  1 year immutable to 1 hour.
* **gtag.js deferred** — the GA library is injected after the `load` event
  ([layout.templ](../internal/web/templates/layout.templ)). The inline
  dataLayer stub, Consent-Mode defaults and `config` call stay in the head,
  queue while the page loads, and flush when the library arrives — no events
  lost, but ~100 KB of Google script leaves the window Lighthouse measures.

## PageSpeed: where the milliseconds actually are

Measured 2026-08-29 from the app's own wire (the keyless PSI API quota was
exhausted that day — re-run the linked reports when needed):

* Static pipeline is already tight: CSS ~11 KB on the wire, htmx ~19 KB,
  app.js ~7 KB, fonts 63 KB total, all `max-age=31536000, immutable` with
  content-hash `?h=` busting, zstd/brotli via Traefik, fonts preloaded with
  `font-display: swap`. The homepage is ~9 KB compressed with no images
  (inline SVG logo), so LCP is the wordmark text — font-gated, already
  preloaded.
* TTFB is dominated by network RTT to the VPS (~250 ms per round trip from
  Europe-adjacent probes; server render itself is ~one RTT after TLS).
  That's geography, not code. If mobile scores from RDC-based users matter
  later, the lever is a CDN/edge cache in front of Traefik, not app work.
* The remaining Lighthouse deductions to expect: render-blocking stylesheet
  (one small file — acceptable; inlining critical CSS would fight the
  immutable-cache design for little gain) and whatever GA costs after the
  deferred load (should now be ~nothing).

Re-measure at <https://pagespeed.web.dev/analysis?url=https%3A%2F%2Fcongopro.com>
(mobile), or `curl "https://www.googleapis.com/pagespeedonline/v5/runPagespeed?url=https://congopro.com&strategy=mobile"`.

### Round 2 (2026-08-29, after the first deploy)

Post-deploy Lighthouse (mobile emulation): **Performance 93 · Accessibility
94 · Best Practices 100 · SEO 100**. The remaining deductions decomposed
into four concrete causes, all fixed the same day — Lighthouse against
production after the round-2 deploy: **100 · 100 · 100 · 100**, CLS
0.151 → 0.001 (the only remaining shift is the cookie banner's 0.001),
confirmed the same day on the official pagespeed.web.dev tool:

* **CLS 0.15 (the entire penalty)** — two causes, both hitting the
  vertically-centered hero. The big one (0.11): the homepage ad used to be
  fetched by app.js *after* the healthz round trip and inserted below the
  hero, shifting the whole centered block up. The ad is now server-rendered
  into the initial HTML (`serveSPA` picks it exactly like the /api/v1/ads
  homepage path; app.js only fetches when the container is empty, e.g.
  clearing back from a `?q=` deep link, and fires the SSR ad's `view_ad`
  impression at boot since it never passes through an htmx swap). The small
  one (0.04): web-font swap reflow, fixed with metric-adjusted local
  fallbacks (`Sora Fallback` / `Inter Fallback` in
  [input.css](../internal/web/css/input.css)) — Arial/Liberation reshaped
  via `size-adjust` + `ascent/descent-override` so the swap occupies
  identical space. **If a font file is ever replaced, recompute the
  values** with fontTools: average a–z+space advance widths
  (English-frequency weighted) of web font vs fallback gives `size-adjust`;
  hhea ascent/descent ÷ UPM ÷ size-adjust gives the overrides.
* **Accessibility: the premium homepage ad** — advertiser-chosen colors
  produced 2.45:1 (label) and 2.65:1 (white CTA text on `#ea8600`), and the
  ad title was an `h3` that broke heading order. Label text is now theme
  ink (color stays on the tint/border), the CTA text color is chosen by
  WCAG luminance (`AdCTATextColor`, pinned in safe_test.go), and the title
  is a styled `p` — an ad has no place in the page outline.
* **Back/forward cache blocked** — the homepage served
  `no-cache, no-store`; Chrome refuses bfcache for `no-store` documents.
  Now `no-cache` (public page, nothing viewer-specific). Authenticated
  layouts keep `no-store`. A second bfcache reason comes from Google's own
  gtag request and is not actionable.
* **Non-passive scroll listener** — the scroll-to-top widget's listener now
  passes `{ passive: true }`.

Not worth chasing: "unused JavaScript ~67 KB" is gtag.js itself (already
deferred past `load`); ~3 KB from app.js being unminified (a JS minifier is
not worth a Node build dependency); htmx's ES5 patterns ("legacy
JavaScript"). Speed Index reflects the late cookie-banner paint, which is
the banner doing its job.

**Lab 100 is not the ranking input.** PageSpeed still reports "the Chrome
User Experience Report does not have sufficient real-world speed data for
this page" — Core Web Vitals only affect ranking once CrUX has enough real
Chrome traffic to populate, and congopro.com is below that threshold at the
URL level. So treat 100/100/100/100 as *the site is not the bottleneck*,
not as a ranking win already banked. The field data that will eventually
appear is measured from RDC mobile networks, where the wins that matter are
the ones already made (no images on the homepage, ~9 KB HTML, immutable
assets) plus, if it ever matters, an edge cache — see the TTFB note above.

## Search Console baseline (exports of 2026-08-29, data through ~08-21 — pre-fix)

The coverage report taken just before the canonical fix shows exactly the
damage the fix targets. Keep these numbers; the buckets should drain against
them over the following weeks:

| Bucket | Pages | What it actually is |
|---|---|---|
| Alternate page with proper canonical | 952 | **The www bug live**: apex `/company/*` pages excluded because their canonical said www. Should flip to Indexed as recrawl sees apex self-canonicals. |
| Duplicate, Google chose different canonical | 252 | www URLs where Google overrode the tag and picked apex anyway. Moves to "Page with redirect". |
| Crawled – currently not indexed | 1385 | Mostly host/lang variants of the same profile splitting signals; expect shrink after consolidation. What remains is thin-content profiles. |
| Page with redirect | 1330 | Legacy `/fr/…`, `/en/…` 301s doing their job; www joins this bucket now. Not a problem. |
| Blocked by robots.txt | 1307 | Stale June entries: legacy `?page=&q=` and `/fr/company/*` URLs blocked by the *old* site's robots.txt; ours blocks nothing. Drains as recrawled. |
| Server error (5xx) | 176 | Real bug, fixed 2026-08-29: company pages 503'd during every deploy's Meilisearch indexing+embedding wait. Profiles now gate on `DataReady` (in-memory data, ready in seconds). |
| Excluded by noindex | 824 | Legacy `/proposals/*` URLs from the old platform (April dates). Ages out; not ours. |
| Not found (404) | 53 | Dead legacy slugs; 404 is the correct answer. |

Search performance (92 days to 08-26): 533 clicks / 32.5k impressions,
month-over-month **doubling** (126→234 clicks, 6.9k→11.9k impressions),
avg position ~9. Every top page is indexed under **www** — the traffic will
migrate to apex URLs as consolidation lands; a dip-and-recover in the Pages
report during the move is normal. Traffic is 74% mobile, 61% RDC. Crawl
stats: www ate 3,491 of 13,735 crawl requests (25% of budget, freed by the
301); "robots.txt not available" was 3.5% of fetches — deploy-restart blips
that pause all crawling; both hosts show "Problems in the past" for it.

## Search Console — property exists for years; what this milestone changes

No setup needed. After deploying this milestone:

1. **Sitemaps** → submit `https://congopro.com/sitemap.xml` alongside the
   old `.gz` entry (which still works — it now serves a real gzip file).
   The new URL is the one robots.txt advertises going forward.
2. First weeks, watch **Indexing → Pages**: the www→apex 301 plus the
   canonical fix will surface as "Page with redirect" / "Duplicate, Google
   chose canonical" entries draining away. That's the cleanup working, not
   a problem. If the property is a URL-prefix property on the `www` host
   specifically, its curve will decline as the apex absorbs the traffic —
   a **Domain** property sees both sides of the move in one graph.
3. The homepage's new `WebSite`+`SearchAction` JSON-LD may take a few
   crawls to show under enhancements; nothing to do besides wait.

## Google Analytics — live for years, tag unchanged

GA4 tag `G-VS32J060KG` in `Layout` with Consent Mode v2: all four signals
default **denied**, upgraded on the cookie banner's "Tout accepter"
(persisted in `localStorage.sb_cookie_consent`). Admin and account layouts
deliberately carry no analytics. Events beyond page_view: `view_item` on
company profiles (also fired by the SPA search flow in app.js).

This milestone only changed *when* gtag.js loads (after `load`), not what
it measures — history and property continuity are untouched. If GA sessions
ever look off after a deploy, check the deferred loader in
[layout.templ](../internal/web/templates/layout.templ) first.
