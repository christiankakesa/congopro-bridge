package ratelimiter

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/time/rate"
)

// trustedProxies holds the CIDR ranges of reverse proxies allowed to set
// X-Forwarded-For/X-Real-IP. Populated once at startup via SetTrustedProxies;
// read-only afterwards, so no locking is needed on the read path.
var trustedProxies []*net.IPNet

// SetTrustedProxies configures which peer addresses are trusted to supply
// client-IP forwarding headers. Call once at startup, before serving traffic.
// Invalid entries are logged and skipped. If cidrs is empty, no proxy is
// trusted and requests always resolve to the direct TCP peer address.
func SetTrustedProxies(cidrs []string) {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if ip := net.ParseIP(c); ip != nil && ip.To4() != nil {
				c += "/32"
			} else if ip != nil {
				c += "/128"
			}
		}
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			log.Warn().Msgf("[ratelimiter] ignoring invalid trusted proxy CIDR %q: %v", c, err)
			continue
		}
		nets = append(nets, n)
	}
	trustedProxies = nets
}

func isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range trustedProxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

type visitor struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	rpm      int // requests per minute
}

func NewRateLimiter(requestsPerMinute int) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*visitor),
		rpm:      requestsPerMinute,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(2 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 5*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

func (rl *RateLimiter) getVisitor(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, exists := rl.visitors[ip]
	if !exists {
		l := rate.NewLimiter(rate.Limit(rl.rpm)/60, rl.rpm)
		rl.visitors[ip] = &visitor{limiter: l, lastSeen: time.Now()}
		return l
	}
	v.lastSeen = time.Now()
	return v.limiter
}

func (rl *RateLimiter) WithRateLimit(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := realIP(r)
		if !rl.getVisitor(ip).Allow() {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		h(w, r)
	}
}

// realIP extracts the client IP. X-Real-IP/X-Forwarded-For are only honored
// when the direct TCP peer is a trusted proxy (see SetTrustedProxies) —
// otherwise a client could set those headers itself to get a fresh rate-limit
// bucket on every request.
func realIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}

	if !isTrustedProxy(net.ParseIP(peer)) {
		return peer
	}

	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return strings.TrimSpace(ip)
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// X-Forwarded-For can be a comma-separated list; first entry is the client,
		// safe to trust here because it came through a proxy we trust.
		return strings.TrimSpace(strings.SplitN(ip, ",", 2)[0])
	}
	return peer
}
