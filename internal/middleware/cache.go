package middleware

import (
	"bytes"
	"io"
	"net/http"

	"github.com/magendooro/magento2-customer-graphql-go/internal/cache"
	"github.com/rs/zerolog/log"
)

// CacheMiddleware caches GraphQL responses in Redis.
// Only caches POST requests to /graphql with successful responses.
// Skips caching for authenticated requests (mutations / customer-specific data).
func CacheMiddleware(c *cache.Client) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if c == nil {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Only cache unauthenticated GraphQL POST requests
			if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
				next.ServeHTTP(w, r)
				return
			}

			// Skip cache for authenticated requests — customer data is personal
			if r.Header.Get("Authorization") != "" {
				next.ServeHTTP(w, r)
				return
			}

			body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			// Skip cache for mutations
			if bytes.Contains(body, []byte("mutation")) {
				next.ServeHTTP(w, r)
				return
			}

			storeCode := r.Header.Get("Store")
			if storeCode == "" {
				storeCode = "default"
			}
			key := cache.CacheKey(storeCode, body)

			if cached, ok := c.Get(r.Context(), key); ok {
				log.Debug().Str("key", key).Msg("cache hit")
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Cache", "HIT")
				w.Write(cached)
				return
			}

			rec := &responseRecorder{ResponseWriter: w, body: &bytes.Buffer{}}
			next.ServeHTTP(rec, r)

			if rec.statusCode == 0 || rec.statusCode == http.StatusOK {
				c.Set(r.Context(), key, rec.body.Bytes())
				w.Header().Set("X-Cache", "MISS")
			}
		})
	}
}

type responseRecorder struct {
	http.ResponseWriter
	body       *bytes.Buffer
	statusCode int
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	r.body.Write(b)
	return r.ResponseWriter.Write(b)
}

func (r *responseRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}
