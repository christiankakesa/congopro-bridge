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
