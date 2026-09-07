package main

import (
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var hashedAssetName = regexp.MustCompile(`^[A-Za-z0-9_.-]+-[A-Za-z0-9_-]{8,}\.(?:js|css|svg|png|jpe?g|webp|woff2?)$`)

// serveStaticAsset serves build-time compressed public assets. API responses
// never enter this path; compression requires a separate packaged resource.
func serveStaticAsset(w http.ResponseWriter, r *http.Request, filename string) {
	switch filepath.Ext(filename) {
	case ".html", ".js", ".css", ".svg":
		compressed, err := os.Open(filename + ".gz")
		if err == nil {
			defer compressed.Close()
			info, err := compressed.Stat()
			if err == nil && info.Mode().IsRegular() {
				w.Header().Add("Vary", "Accept-Encoding")
				if acceptsGzip(r.Header.Get("Accept-Encoding")) {
					w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(filename)))
					w.Header().Set("Content-Encoding", "gzip")
					if r.Header.Get("Range") == "" {
						w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
					}
					http.ServeContent(w, r, filepath.Base(filename), info.ModTime(), compressed)
					return
				}
			}
		}
	}
	http.ServeFile(w, r, filename)
}

func acceptsGzip(header string) bool {
	wildcard := false
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(item, ";")
		encoding := strings.ToLower(strings.TrimSpace(parts[0]))
		if encoding != "gzip" && encoding != "*" {
			continue
		}
		quality := 1.0
		for _, parameter := range parts[1:] {
			name, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(name), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || parsed < 0 || parsed > 1 {
				quality = 0
			} else {
				quality = parsed
			}
		}
		if encoding == "gzip" {
			return quality > 0
		}
		wildcard = quality > 0
	}
	return wildcard
}
