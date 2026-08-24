// Package cors mirrors SecurityConfig.corsConfigurationSource() — nothing
// calls this service directly from a browser anymore (the gateway sits in
// front, server-to-server, no Origin header involved), so this only matters
// for local dev/testing this service directly.
package cors

import (
	"net/http"
	"slices"
)

var (
	allowedOrigins = []string{"http://localhost:3000"}
	allowedMethods = "GET, POST, PUT, DELETE, OPTIONS"
	allowedHeaders = "Authorization, Content-Type, X-Requested-With"
)

// Middleware wraps a handler, applying the CORS policy above to every request.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && slices.Contains(allowedOrigins, origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
