package mcphttp

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// ipLimiter tracks per-IP rate limiters with automatic cleanup.
type ipLimiter struct {
	mu       sync.Mutex
	limiters map[string]*limiterEntry
	rate     rate.Limit
	burst    int
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// newIPLimiter creates a per-IP rate limiter.
// rps is requests per second; burst is the maximum burst size.
func newIPLimiter(rps float64, burst int) *ipLimiter {
	ipl := &ipLimiter{
		limiters: make(map[string]*limiterEntry),
		rate:     rate.Limit(rps),
		burst:    burst,
	}
	go ipl.cleanup()
	return ipl
}

// allow checks whether the given IP is within its rate limit.
func (ipl *ipLimiter) allow(ip string) bool {
	ipl.mu.Lock()
	entry, exists := ipl.limiters[ip]
	if !exists {
		entry = &limiterEntry{
			limiter: rate.NewLimiter(ipl.rate, ipl.burst),
		}
		ipl.limiters[ip] = entry
	}
	entry.lastSeen = time.Now()
	ipl.mu.Unlock()
	return entry.limiter.Allow()
}

// cleanup removes stale entries every 5 minutes.
func (ipl *ipLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		ipl.mu.Lock()
		cutoff := time.Now().Add(-10 * time.Minute)
		for ip, entry := range ipl.limiters {
			if entry.lastSeen.Before(cutoff) {
				delete(ipl.limiters, ip)
			}
		}
		ipl.mu.Unlock()
	}
}

// RateLimit wraps an http.Handler with per-IP rate limiting.
// Requests that exceed the limit receive a 429 Too Many Requests response.
func RateLimit(rps float64, burst int, next http.Handler) http.Handler {
	limiter := newIPLimiter(rps, burst)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := extractIP(r)
		if !limiter.allow(ip) {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// extractIP returns the client IP from X-Forwarded-For or RemoteAddr.
func extractIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Use the first IP in the chain (client IP).
		for i := range len(xff) {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	// Strip port from RemoteAddr.
	addr := r.RemoteAddr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
