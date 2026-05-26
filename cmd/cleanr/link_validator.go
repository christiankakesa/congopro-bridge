package main

// link_validator.go
//
// LinkValidatorStep checks every URL in the configured fields and clears the
// field value when the URL is definitively dead.
//
// Decision matrix
// ───────────────
//  Signal                              → Action
//  DNS "no such host"                  → CLEAR  (domain gone / for sale)
//  DNS timeout                         → KEEP   (our resolver had a bad day)
//  TCP connection refused              → CLEAR  (nothing listening, domain parked)
//  TCP / TLS timeout                   → KEEP   (could be our network)
//  TLS error (cert invalid / expired)  → KEEP   (site may still exist, just broken TLS)
//  HTTP 200–399                        → KEEP   (alive)
//  HTTP 404 / 410                      → CLEAR  (confirmed gone)
//  HTTP 405 on HEAD                    → retry with GET (server doesn't allow HEAD)
//  HTTP 429 / 503                      → KEEP   (rate-limited or temporarily down)
//  HTTP 5xx (other)                    → KEEP   (server-side problem, not our call)

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

var deadStatuses = map[int]string{
	http.StatusNotFound: "404 Not Found",
	http.StatusGone:     "410 Gone",
}

type LinkValidatorStep struct {
	Fields     []string
	MaxWorkers int
}

func (s *LinkValidatorStep) Name() string { return "404 Link Validator" }

func (s *LinkValidatorStep) Process(ctx context.Context, records []map[string]interface{}) ([]map[string]interface{}, error) {
	client := &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, s.MaxWorkers)

	for _, record := range records {
		for _, field := range s.Fields {
			val, ok := record[field].(string)
			if !ok || val == "" || !strings.HasPrefix(val, "http") {
				continue
			}

			wg.Add(1)
			semaphore <- struct{}{}

			go func(rec map[string]interface{}, f, url string) {
				defer wg.Done()
				defer func() { <-semaphore }()

				if isDead, reason := checkURL(ctx, client, url); isDead {
					log.Info().
						Str("url", url).
						Str("reason", reason).
						Msgf("[link validator] clearing dead URL")
					rec[f] = ""
				}
			}(record, field, val)
		}
	}

	wg.Wait()
	return records, nil
}

func checkURL(ctx context.Context, client *http.Client, url string) (bool, string) {
	dead, reason, retryWithGet := doRequest(ctx, client, http.MethodHead, url)

	if retryWithGet {
		dead, reason, _ = doRequest(ctx, client, http.MethodGet, url)
	}

	return dead, reason
}

// doRequest performs a single HTTP request and classifies the outcome.
//
// Return values:
//   - dead        – true when the URL is definitively gone
//   - reason      – human-readable explanation (for logging)
//   - retryAsGet  – true when the caller should retry using GET instead of HEAD
func doRequest(ctx context.Context, client *http.Client, method, url string) (dead bool, reason string, retryAsGet bool) {
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		return true, "malformed URL: " + err.Error(), false
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CongoproBot/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return classifyNetworkError(err)
	}
	defer resp.Body.Close()

	if method == http.MethodHead && resp.StatusCode == http.StatusMethodNotAllowed {
		return false, "", true
	}

	if label, isDead := deadStatuses[resp.StatusCode]; isDead {
		return true, label, false
	}

	return false, "", false
}

func classifyNetworkError(err error) (dead bool, reason string, retryAsGet bool) {
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		if dnsErr.IsNotFound {
			return true, "DNS: domain not found (" + dnsErr.Name + ")", false
		}
		if dnsErr.IsTimeout {
			log.Warn().Str("host", dnsErr.Name).Msg("[link validator] DNS timeout, keeping URL")
			return false, "", false
		}
		log.Warn().Str("err", dnsErr.Error()).Msg("[link validator] DNS error, keeping URL")

		return false, "", false
	}

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		if opErr.Op == "dial" {
			inner := opErr.Unwrap()
			if inner != nil && strings.Contains(inner.Error(), "connection refused") {
				return true, "TCP: connection refused", false
			}
		}
		if opErr.Timeout() {
			log.Warn().Str("url", opErr.Addr.String()).Msg("[link validator] TCP timeout, keeping URL")
			return false, "", false
		}
		log.Warn().Str("err", opErr.Error()).Msg("[link validator] network op error, keeping URL")

		return false, "", false
	}
	log.Warn().Str("err", err.Error()).Msg("[link validator] unclassified error, keeping URL")

	return false, "", false
}
