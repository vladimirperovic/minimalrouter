package main

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStaticAssetNegotiatesPackagedGzip(t *testing.T) {
	root := t.TempDir()
	plain := []byte(strings.Repeat("body { color: blue; }\n", 100))
	if err := os.WriteFile(filepath.Join(root, "app.css"), plain, 0644); err != nil {
		t.Fatal(err)
	}
	var packed bytes.Buffer
	writer := gzip.NewWriter(&packed)
	if _, err := writer.Write(plain); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.css.gz"), packed.Bytes(), 0644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		header     string
		compressed bool
	}{
		{"", false}, {"br", false}, {"gzip", true}, {"gzip;q=0", false}, {"br, gzip;q=0.7", true}, {"*;q=1, gzip;q=0", false}, {"*;q=0.5", true}, {"gzip;q=bogus", false},
	} {
		t.Run(tc.header, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/app.css", nil)
			request.Header.Set("Accept-Encoding", tc.header)
			response := httptest.NewRecorder()
			staticHandler(root).ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d", response.Code)
			}
			if response.Header().Get("Vary") != "Accept-Encoding" || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/css") {
				t.Fatal("representation negotiation lost MIME/Vary")
			}
			body := response.Body.Bytes()
			if tc.compressed {
				if response.Header().Get("Content-Encoding") != "gzip" {
					t.Fatal("gzip absent")
				}
				reader, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, err = io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
			} else if response.Header().Get("Content-Encoding") != "" {
				t.Fatal("unacceptable encoding served")
			}
			if !bytes.Equal(body, plain) {
				t.Fatal("decoded bytes changed")
			}
		})
	}
	requestRange := httptest.NewRequest(http.MethodGet, "/app.css", nil)
	requestRange.Header.Set("Accept-Encoding", "gzip")
	requestRange.Header.Set("Range", "bytes=0-9")
	partial := httptest.NewRecorder()
	staticHandler(root).ServeHTTP(partial, requestRange)
	if partial.Code != http.StatusPartialContent || !bytes.Equal(partial.Body.Bytes(), packed.Bytes()[:10]) {
		t.Fatal("compressed byte range changed")
	}
	request := httptest.NewRequest(http.MethodHead, "/app.css", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	response := httptest.NewRecorder()
	staticHandler(root).ServeHTTP(response, request)
	if response.Body.Len() != 0 || response.Header().Get("Content-Length") != strconv.Itoa(packed.Len()) {
		t.Fatal("HEAD did not describe compressed representation")
	}
}

func TestStaticGzipDoesNotCompressAPIOrInventMissingAssets(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html>"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/api/v1/config", "/missing.js"} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		request.Header.Set("Accept-Encoding", "gzip")
		response := httptest.NewRecorder()
		staticHandler(root).ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || response.Header().Get("Content-Encoding") != "" {
			t.Fatalf("unexpected fallback for %s", target)
		}
	}
	response := httptest.NewRecorder()
	staticHandler(root).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("index caching policy changed")
	}
}

func TestOnlyContentNamedBuildAssetsAreImmutable(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"assets/index-Abc123_X.js", "assets/runtime.js", "settings-Abc123_X.js"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("/* static */"), 0644); err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		staticHandler(root).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/"+name, nil))
		immutable := strings.Contains(response.Header().Get("Cache-Control"), "immutable")
		if immutable != (name == "assets/index-Abc123_X.js") {
			t.Fatalf("incorrect cache policy for %s", name)
		}
	}
}
