package firmware

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

type archiveEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
}

func writeTestArchive(t *testing.T, entries []archiveEntry) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "release.tar.gz")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	gz := gzip.NewWriter(file)
	tw := tar.NewWriter(gz)
	for _, entry := range entries {
		header := &tar.Header{Name: entry.name, Typeflag: entry.typeflag, Mode: entry.mode, Size: int64(len(entry.body))}
		if entry.typeflag == tar.TypeDir {
			header.Size = 0
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.body != "" {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func TestExtractReleaseArchivePreservesReviewedModes(t *testing.T) {
	archivePath := writeTestArchive(t, []archiveEntry{
		{name: "minimalrouter-linux-amd64", typeflag: tar.TypeDir, mode: 0o755},
		{name: "minimalrouter-linux-amd64/bin", typeflag: tar.TypeDir, mode: 0o755},
		{name: "minimalrouter-linux-amd64/bin/routerd-amd64", typeflag: tar.TypeReg, mode: 0o755, body: "binary"},
		{name: "minimalrouter-linux-amd64/web/dist/index.html", typeflag: tar.TypeReg, mode: 0o644, body: "html"},
	})
	destination := t.TempDir()
	payload, err := extractReleaseArchive(archivePath, destination, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if payload != filepath.Join(destination, "minimalrouter-linux-amd64") {
		t.Fatalf("unexpected payload root %q", payload)
	}
	info, err := os.Stat(filepath.Join(payload, "bin", "routerd-amd64"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("executable mode = %o, want 755", got)
	}
}

func TestExtractReleaseArchiveRejectsTraversalAndLinks(t *testing.T) {
	for name, entries := range map[string][]archiveEntry{
		"traversal": {{name: "../escape", typeflag: tar.TypeReg, mode: 0o644, body: "bad"}},
		"symlink": {{name: "minimalrouter-linux-amd64/link", typeflag: tar.TypeSymlink, mode: 0o777}},
		"other-root": {{name: "other/file", typeflag: tar.TypeReg, mode: 0o644, body: "bad"}},
	} {
		t.Run(name, func(t *testing.T) {
			archivePath := writeTestArchive(t, entries)
			if _, err := extractReleaseArchive(archivePath, t.TempDir(), "amd64"); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}
