// Congopro Bridge — public SPA coordinator (home + search results).
// Extracted verbatim from the retired index.html inline script (B2 shell
// unification). Served content-hashed and cached immutable; loaded only by
// templates/home.templ. The scroll-to-top widget and cookie-consent logic
// are NOT here — layout.templ's scrollToTop/cookieConsent components own
// them for every Layout page, this one included.

// ── Config ────────────────────────────────────────────
const API_BASE = "";

// ── DOM ───────────────────────────────────────────────
const homepage = document.getElementById("homepage");
const resultsPage = document.getElementById("resultsPage");
const homeInput = document.getElementById("homeInput");
const homeClear = document.getElementById("homeClear");
const homeStatus = document.getElementById("homeStatus");
const homeSearchIcon = document.getElementById("homeSearchIcon");
const homeSpinner = document.getElementById("homeSpinner");
const topInput = document.getElementById("topInput");
const topClear = document.getElementById("topClear");
const topSearchIcon = document.getElementById("topSearchIcon");
const topSpinner = document.getElementById("topSpinner");
const logoLink = document.getElementById("logoLink");
const resultsList = document.getElementById("resultsList");
// statsBar, emptyState, adContainer and resultsLabel are NOT cached
// here — each gets replaced wholesale (hx-swap-oob="outerHTML" or
// hx-swap="outerHTML") by htmx responses, so a cached reference would
// go stale (pointing at a detached node) after the first swap. Always
// look them up fresh at the point of use instead.

// ── Utilities ─────────────────────────────────────────
// esc()/validateURL()/safePhone()/safeEmail() are gone — the last
// client-side renderers that needed them (ads, results rows, the
// footer) all moved server-side in Phase 2 / the shared-footer change.
// Their Go equivalents live in internal/web/templates/safe.go.

const Analytics = {
  /**
   * Enregistre l'affichage des résultats de recherche
   * @param {Array} results - Le tableau des entreprises retournées
   * @param {string} searchTerm - Le mot-clé recherché (optionnel)
   */
  trackSearchResults: function (results, searchTerm = "") {
    if (typeof gtag !== "function") return;

    const topResults = results.map((company, index) => ({
      item_id: company.name_seo || company.name,
      item_name: company.name,
      item_category: company.activity || "Non précisé",
      index: index + 1,
    }));

    gtag("event", "view_item_list", {
      item_list_id: "search_results",
      item_list_name: "Résultats de recherche",
      search_term: searchTerm,
      items: topResults,
    });
  },

  /**
   * Enregistre l'affichage d'un profil d'entreprise spécifique
   * @param {Object} company - L'objet entreprise
   */
  trackCompanyView: function (company) {
    if (typeof gtag !== "function") return;

    gtag("event", "view_item", {
      currency: "USD",
      value: 0,
      items: [
        {
          item_id: company.name_seo || company.name,
          item_name: company.name,
          item_category: company.activity || "Non précisé",
        },
      ],
    });
  },
};

// ── View switching ────────────────────────────────────
function showHome() {
  if (window.location.pathname !== "/" || window.location.search !== "") {
    history.pushState(null, "", "/");
  }

  // cleaning
  document.getElementById("aiContent").innerHTML = "";

  window.scrollTo(0, 0);
  resultsPage.classList.add("hidden");
  homepage.style.display = "flex";
  resultsList.innerHTML = "";
  document.getElementById("statsBar")?.classList.add("hidden");
  document.getElementById("resultsLabel")?.classList.add("hidden");
  stopAdRotation();
  loadHomepageAd();
  document.getElementById("emptyState")?.classList.add("hidden");
  homeInput.value = "";
  homeClear.classList.add("hidden");
  homeInput.focus();
}
function showResults(q, fromHome) {
  homepage.style.display = "none";
  resultsPage.classList.remove("hidden");
  if (fromHome) {
    topInput.value = q;
    topInput.focus();
    const len = topInput.value.length;
    topInput.setSelectionRange(len, len);
  }
  topClear.classList.toggle("hidden", !topInput.value);
}

logoLink.addEventListener("click", (e) => {
  e.preventDefault();
  showHome();
});

// ── Spinner ───────────────────────────────────────────
function setLoading(on, inHome) {
  const icon = inHome ? homeSearchIcon : topSearchIcon;
  const spin = inHome ? homeSpinner : topSpinner;
  icon.classList.toggle("hidden", on);
  spin.classList.toggle("hidden", !on);
}

// ── Search (htmx-driven) ────────────────────────────────
// homeInput/topInput carry hx-get/hx-trigger/hx-target — htmx issues
// the request and swaps #resultsList. This coordinator handles the
// parts htmx doesn't: showing the results view the instant a search
// starts (not after the response arrives), the loading spinner, and —
// once the swap lands — GA tracking, the AI-overview box, and ads,
// all of which depend on the outcome of the search.
let lastSearchQuery = "";

// Below-3-chars guard for the debounced "input" trigger. This can't
// be expressed as an hx-trigger [expression] filter — htmx evaluates
// those via new Function()/eval, which our CSP (no 'unsafe-eval')
// correctly blocks. Enter-triggered and shared-link searches bypass
// this entirely: they call htmx.ajax() directly instead of going
// through the declarative trigger, so this only ever gates typing.
document.body.addEventListener("htmx:configRequest", (e) => {
  const elt = e.detail.elt;
  const isDeclaredInputTrigger =
    e.detail.triggeringEvent && e.detail.triggeringEvent.type === "input";
  if (
    isDeclaredInputTrigger &&
    (elt === homeInput || elt === topInput) &&
    elt.value.trim().length < 3
  ) {
    e.preventDefault();
  }
});

function searchOnEnter(input) {
  const q = input.value.trim();
  if (!q) return;
  htmx.ajax("GET", "/api/v1/search?q=" + encodeURIComponent(q), {
    source: input,
    target: "#resultsList",
    swap: "innerHTML",
  });
}
homeInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") searchOnEnter(homeInput);
});
topInput.addEventListener("keydown", (e) => {
  if (e.key === "Enter") searchOnEnter(topInput);
});

document.body.addEventListener("htmx:beforeRequest", (e) => {
  const elt = e.detail.elt;
  if (elt !== homeInput && elt !== topInput) return;
  const q = elt.value.trim();
  lastSearchQuery = q;
  const fromHome = elt === homeInput;
  showResults(q, fromHome);
  setLoading(true, fromHome);
});

document.body.addEventListener("htmx:afterRequest", (e) => {
  const elt = e.detail.elt;
  if (elt !== homeInput && elt !== topInput) return;
  setLoading(false, elt === homeInput);
});

document.body.addEventListener("htmx:afterSwap", (e) => {
  if (e.detail.target !== resultsList) return;
  const q = lastSearchQuery;

  const items = resultsList.querySelectorAll("[data-item-id]");
  if (items.length === 1) {
    const el = items[0];
    Analytics.trackCompanyView({
      name_seo: el.getAttribute("data-item-id"),
      name: el.getAttribute("data-item-name"),
      activity: el.getAttribute("data-item-category"),
    });
  } else if (items.length > 1) {
    const results = Array.from(items).map((el) => ({
      name_seo: el.getAttribute("data-item-id"),
      name: el.getAttribute("data-item-name"),
      activity: el.getAttribute("data-item-category"),
    }));
    Analytics.trackSearchResults(results, q);
  }

  const aiBox = document.getElementById("aiOverview");
  if (items.length > 0) {
    aiBox.classList.remove("hidden");

    if (isComplexQuery(q)) {
      triggerAI(q);
    } else {
      document.getElementById("aiTriggerBtn").classList.remove("hidden");
      document.getElementById("aiTriggerBtn").classList.add("flex");
      document.getElementById("aiLoading").classList.add("hidden");
      document.getElementById("aiLoading").classList.remove("flex");
      document.getElementById("aiContent").classList.add("hidden");

      document.getElementById("aiTriggerBtn").onclick = () =>
        triggerAI(q);
    }
  } else {
    aiBox.classList.add("hidden");
  }

  fetchAds(q);
});

// ── Home input ────────────────────────────────────────
homeInput.addEventListener("input", () => {
  const q = homeInput.value.trim();
  homeClear.classList.toggle("hidden", !q);
  if (q.length === 0) showHome();
});
homeClear.addEventListener("click", () => {
  homeInput.value = "";
  homeClear.classList.add("hidden");
  homeInput.focus();
});

// ── Top bar input ─────────────────────────────────────
topInput.addEventListener("input", () => {
  const q = topInput.value.trim();
  topClear.classList.toggle("hidden", !q);
  if (q.length === 0) showHome();
});
topClear.addEventListener("click", () => {
  topInput.value = "";
  topClear.classList.add("hidden");
  showHome();
});

// ── The Router ─────────────────────────────────────────
// This SPA only owns "/" and search results (/?q=…); every other path
// (/help, /account, /company/*, …) is a real server-rendered page (see
// internal/web/templates). The click handler further down only
// intercepts links whose pathname is exactly "/" — everything else
// falls through to a normal browser page load.
function handleRoute() {
  homepage.style.display = "none";
  resultsPage.classList.add("hidden");

  // Route: Home OR a Shared Search Link
  const urlParams = new URLSearchParams(window.location.search);
  const queryParam = urlParams.get("q");

  if (queryParam) {
    topInput.value = queryParam;
    topClear.classList.remove("hidden");
    // Programmatic equivalent of the input's own hx-get — source:
    // topInput makes this participate in the normal htmx event
    // lifecycle (htmx:beforeRequest/afterSwap) just like typing does.
    htmx.ajax("GET", "/api/v1/search?q=" + encodeURIComponent(queryParam), {
      source: topInput,
      target: "#resultsList",
      swap: "innerHTML",
    });
  } else {
    showHome();
  }
}

function isComplexQuery(q) {
  if (!q || q.length < 5) return false;

  const text = q.trim().toLowerCase();
  const words = text.split(/\s+/);
  const wordCount = words.length;

  const interrogatives = [
    "what",
    "where",
    "when",
    "why",
    "how",
    "which",
    "who",
    "whom", // end English
    "que",
    "quoi",
    "où",
    "quand",
    "pourquoi",
    "comment",
    "quel",
    "quelle",
    "quels",
    "quelles",
    "qui",
    "est-ce que",
    "qu'est-ce",
  ];
  const hasInterrogative = interrogatives.some(
    (w) =>
      text.startsWith(w) ||
      text.includes(" " + w) ||
      text.includes(w + " "),
  );

  const actionVerbs = [
    "compare",
    "explain",
    "describe",
    "summarize",
    "recommend",
    "list",
    "analyse",
    "analyze",
    "evaluate",
    "discuss",
    "contrast",
    "detail",
    "enumerate",
    "justify",
    "show", // end English
    "comparer",
    "expliquer",
    "décrire",
    "résumer",
    "recommander",
    "lister",
    "analyser",
    "évaluer",
    "discuter",
    "contraster",
    "détailler",
    "énumérer",
    "justifier",
    "montre",
  ];
  const hasActionVerb = actionVerbs.some((v) => text.includes(v));

  const rankingTerms = [
    "top",
    "best",
    "highest",
    "lowest",
    "most",
    "least",
    "better",
    "cheapest",
    "largest",
    "smallest",
    "meilleur",
    "meilleure",
    "meilleurs",
    "meilleures",
    "plus haut",
    "plus élevé",
    "plus bas",
    "moins cher",
    "le plus",
    "la plus",
    "les plus",
    "le moins",
    "la moins",
    "mieux",
    "pire",
  ];
  const hasRanking = rankingTerms.some((t) => text.includes(t));

  const endsWithQuestion = q.trim().endsWith("?");

  const isLong = wordCount >= 5;

  const capitalised = (q.match(/\b[A-ZÀ-ÖØ-öø-ÿ][a-zà-öø-ÿ]+\b/g) || [])
    .length;
  const hasEntities = capitalised >= 2;

  let score = 0;
  if (hasInterrogative) score += 2;
  if (hasActionVerb) score += 2;
  if (hasRanking) score += 1;
  if (isLong) score += 1;
  if (hasEntities) score += 1;

  return score >= 3 && endsWithQuestion;
}

// pendingAiQuery tracks which query the in-flight AI request is for.
// Answers are slow (LLM inference), so a search that starts while one
// is still generating must not let the stale answer clobber whatever
// the newer search already displayed — htmx:beforeSwap below discards
// it if lastSearchQuery has since moved on, replacing the old
// AbortController-based cancellation.
let pendingAiQuery = "";

function triggerAI(q) {
  const triggerBtn = document.getElementById("aiTriggerBtn");
  const loading = document.getElementById("aiLoading");
  const content = document.getElementById("aiContent");

  triggerBtn.classList.add("hidden");
  loading.classList.remove("hidden");
  loading.classList.add("flex");
  content.classList.add("hidden");

  pendingAiQuery = q;
  htmx.ajax("GET", `${API_BASE}/api/v1/ask?q=${encodeURIComponent(q)}`, {
    target: "#aiContent",
    swap: "innerHTML",
  });
}

document.body.addEventListener("htmx:beforeSwap", (e) => {
  if (!e.detail.target || e.detail.target.id !== "aiContent") return;
  if (pendingAiQuery !== lastSearchQuery) {
    e.detail.shouldSwap = false;
    return;
  }
  document.getElementById("aiLoading").classList.add("hidden");
  document.getElementById("aiLoading").classList.remove("flex");
  e.detail.target.classList.remove("hidden");
});

document.body.addEventListener("htmx:responseError", (e) => {
  if (!e.detail.target || e.detail.target.id !== "aiContent") return;
  const loading = document.getElementById("aiLoading");
  const triggerBtn = document.getElementById("aiTriggerBtn");
  loading.classList.add("hidden");
  loading.classList.remove("flex");
  triggerBtn.classList.remove("hidden");
  triggerBtn.textContent = "Générer à nouveau";
});

// ── Override standard links for SPA Navigation ──────────
document.addEventListener("click", async (e) => {
  const adLink = event.target.closest("[data-track-click='true']");
  if (adLink && typeof gtag === "function") {
    const adId = adLink.getAttribute("data-ad-id");
    const adTitle = adLink.getAttribute("data-ad-title");
    const adPlacement = adLink.getAttribute("data-ad-placement");

    gtag("event", "click_ad", {
      ad_id: adId,
      ad_title: adTitle,
      ad_placement: adPlacement,
    });

    return;
  }

  const shareBtn = e.target.closest(".js-share-btn");
  if (shareBtn) {
    e.preventDefault();

    const companyTitle =
      shareBtn.getAttribute("data-title") || "Congopro Bridge";
    const currentUrl = window.location.href;
    if (navigator.share) {
      try {
        await navigator.share({
          title: companyTitle,
          url: currentUrl,
        });
      } catch (err) {
        if (err.name !== "AbortError") {
          console.error("Erreur de partage:", err);
        }
      }
    } else {
      try {
        await navigator.clipboard.writeText(currentUrl);
        alert("Lien copié dans le presse-papier !");
      } catch (err) {
        console.error("Erreur de copie:", err);
      }
    }

    return;
  }

  // This SPA only owns "/" (home and /?q= shared searches). Every
  // other same-host path — /help, /account, /company/*, … — is a real
  // server-rendered page and must fall through to a normal browser
  // navigation. Intercepting used to be deny-list based, which broke
  // each time a new server-rendered page shipped (e.g. /account).
  const link = e.target.closest("a");
  if (
    link &&
    link.host === window.location.host &&
    !link.hasAttribute("target") &&
    link.pathname === "/"
  ) {
    e.preventDefault();
    history.pushState(null, "", link.href);
    handleRoute();
  }
});

window.addEventListener("popstate", handleRoute);

// ── Health polling & Boot ─────────────────────────────
const HEALTH_BACKOFF = {
  initialMs: 1000,
  maxMs: 30000,
  factor: 2,
  attempt: 0,
};
function nextBackoffDelay() {
  const delay = Math.min(
    HEALTH_BACKOFF.initialMs *
      Math.pow(HEALTH_BACKOFF.factor, HEALTH_BACKOFF.attempt),
    HEALTH_BACKOFF.maxMs,
  );
  HEALTH_BACKOFF.attempt++;
  return delay;
}
async function checkHealth() {
  try {
    const res = await fetch(`${API_BASE}/api/v1/healthz`);
    const data = await res.json();
    // Any status other than "indexing" means the server has finished
    // starting up — "ready" or "degraded" (indexing failed but static
    // pages and company profiles still work off in-memory data).
    if (data.status !== "indexing") {
      HEALTH_BACKOFF.attempt = 0;
      homeStatus.textContent =
        data.status === "degraded"
          ? "⚠️ Recherche temporairement indisponible."
          : "";
      handleRoute();
      return;
    }

    const delay = nextBackoffDelay();
    homeStatus.textContent = "⏳ Indexation en cours…";
    setTimeout(checkHealth, delay);
  } catch {
    const delay = nextBackoffDelay();
    if (HEALTH_BACKOFF.attempt > 8) {
      const msg = document.createTextNode("⚠️ Serveur inaccessible. ");
      const btn = document.createElement("button");
      btn.textContent = "Recharger la page";
      btn.className = "underline hover:text-gray-600";
      btn.addEventListener("click", () => location.reload());
      homeStatus.innerHTML = "";
      homeStatus.appendChild(msg);
      homeStatus.appendChild(btn);
      return;
    }
    homeStatus.textContent =
      "⚠️ Impossible de joindre le serveur. Nouvelle tentative…";
    setTimeout(checkHealth, delay);
  }
}

// ── Ad engine (htmx-driven) ─────────────────────────────
// Rendering, color/URL validation and slot selection now happen
// server-side (internal/web/templates/ads.templ). Rotation on the
// results page is a self-perpetuating hx-trigger="every Ns" (server-configured) baked
// into each server-rendered replacement rather than a client-side
// timer replaying a cached pool — a deliberate simplification: more
// requests, no cursor state to keep in sync.
function loadHomepageAd() {
  htmx.ajax("GET", "/api/v1/ads?q=", {
    target: "#homepageAdContainer",
    swap: "outerHTML",
  });
}
function fetchAds(q) {
  htmx.ajax("GET", `${API_BASE}/api/v1/ads?q=${encodeURIComponent(q)}`, {
    target: "#adContainer",
    swap: "outerHTML",
  });
}
function stopAdRotation() {
  const container = document.getElementById("adContainer");
  container.classList.add("hidden");
  container.innerHTML = "";
  document.getElementById("resultsLabel")?.classList.add("hidden");
}

// GA4 ad impressions: fire once per unique ad shown, deduped since a
// single ad can carry more than one tracked link (e.g. the homepage
// card's title and its "Découvrir" button).
document.body.addEventListener("htmx:afterSwap", (e) => {
  const target = e.detail.target;
  if (
    !target ||
    (target.id !== "adContainer" && target.id !== "homepageAdContainer")
  )
    return;
  if (typeof gtag !== "function") return;
  const seen = new Set();
  target.querySelectorAll("[data-track-click='true']").forEach((el) => {
    const id = el.getAttribute("data-ad-id");
    if (seen.has(id)) return;
    seen.add(id);
    gtag("event", "view_ad", {
      ad_id: id,
      ad_title: el.getAttribute("data-ad-title"),
      ad_placement: el.getAttribute("data-ad-placement"),
    });
  });
});

// Don't poll for new results-page ads while the results page itself
// is hidden (i.e. back on the homepage) — the fragment's own
// hx-trigger="every Ns" has no [visible]-style guard available (that
// syntax needs eval, which our CSP blocks), so this is the
// CSP-compliant equivalent.
document.body.addEventListener("htmx:configRequest", (e) => {
  if (
    e.detail.elt &&
    e.detail.elt.id === "adContainer" &&
    resultsPage.classList.contains("hidden")
  ) {
    e.preventDefault();
  }
});

// A slow-to-resolve #adContainer fetch (started while search results
// were showing) can still land after the user has since cleared back
// to the homepage — since this is an outerHTML swap, it would
// otherwise resurrect the container instead of leaving it hidden.
document.body.addEventListener("htmx:beforeSwap", (e) => {
  if (
    e.detail.target &&
    e.detail.target.id === "adContainer" &&
    resultsPage.classList.contains("hidden")
  ) {
    e.detail.shouldSwap = false;
  }
});

// ── Boot ──────────────────────────────────────────────
homeStatus.textContent = "⏳ Connexion en cours…";
checkHealth();
