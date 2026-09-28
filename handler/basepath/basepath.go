// Package basepath adapts root-relative HTTP routes for a configured public path.
package basepath

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
)

var validPath = regexp.MustCompile(`^(/[A-Za-z0-9_-]+)+$`)

// Normalize accepts both "drone" and "/drone/", without URL or escaping syntax.
func Normalize(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return "", nil
	}
	value = "/" + strings.Trim(value, "/")
	if !validPath.MatchString(value) {
		return "", fmt.Errorf("invalid DRONE_SERVER_BASE_PATH: use path segments containing letters, numbers, hyphens or underscores")
	}
	return value, nil
}

func Prefix(base, target string) string {
	if base == "" || !strings.HasPrefix(target, "/") || strings.HasPrefix(target, "//") {
		return target
	}
	if target == base || strings.HasPrefix(target, base+"/") || strings.HasPrefix(target, base+"?") {
		return target
	}
	return base + target
}

// Middleware accepts a preserved prefix, or root-relative requests from
// Traefik StripPrefix. X-Forwarded-Prefix only distinguishes already-stripped
// requests; it can never select a public prefix other than the configured one.
func Middleware(base string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if base == "" {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Forwarded-Prefix") != base && (r.URL.Path == base || strings.HasPrefix(r.URL.Path, base+"/")) {
				r = r.Clone(r.Context())
				r.URL.Path = strings.TrimPrefix(r.URL.Path, base)
				if r.URL.RawPath != "" {
					r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, base)
				}
				if r.URL.Path == "" {
					r.URL.Path = "/"
				}
			}
			next.ServeHTTP(&responseWriter{ResponseWriter: w, base: base}, r)
		})
	}
}

// Headers are rewritten without buffering bodies, including SSE log streams.
type responseWriter struct {
	http.ResponseWriter
	base  string
	wrote bool
}

func (w *responseWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	if code >= 100 && code < 200 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.wrote = true
	h := w.Header()
	if location := h.Get("Location"); location != "" {
		h.Set("Location", Prefix(w.base, location))
	}
	cookies := h.Values("Set-Cookie")
	h.Del("Set-Cookie")
	for _, cookie := range cookies {
		parts := strings.Split(cookie, ";")
		hasPath := false
		for i, part := range parts {
			key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && strings.EqualFold(key, "Path") && strings.HasPrefix(value, "/") {
				hasPath = true
				path := Prefix(w.base, value)
				if value == "/" {
					path = w.base
				}
				parts[i] = " Path=" + path
			}
		}
		if !hasPath {
			parts = append(parts, " Path="+w.base)
		}
		h.Add("Set-Cookie", strings.Join(parts, ";"))
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *responseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(w.ResponseWriter).Hijack()
}
