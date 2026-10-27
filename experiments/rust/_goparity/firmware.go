package main

import (
	"bufio"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"

	"github.com/vladimirperovic/minimalrouter/internal/firmware"
)

// firmwareCase is one Go-signed manifest. Ed25519 is deterministic, so the
// Rust port must reproduce the exact signature from the same key, which
// proves the canonical signed payload is byte-identical.
type firmwareCase struct {
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	Manifest   string `json:"manifest"`
	Indented   string `json:"indented"`
	Signature  string `json:"signature"`
}

type versionCase struct {
	Left    string `json:"left"`
	Right   string `json:"right"`
	Compare int    `json:"compare"`
	Error   bool   `json:"error"`
	Valid   bool   `json:"valid"`
}

var nameAlphabet = []string{"a", "Z", "9", "-", ".", "/", "_", "<", ">", "&", "é", "ß", " ", " ", "\"", "\\", " ", "\t", "日", "😀"}

func randomName(rng *rand.Rand) string {
	var b strings.Builder
	for i := 0; i < 1+rng.Intn(24); i++ {
		b.WriteString(nameAlphabet[rng.Intn(len(nameAlphabet))])
	}
	return b.String()
}

var versionParts = []string{"0", "1", "2", "10", "01", "v", ".", "-", "+", "rc", "beta", "alpha", "x", "007", "3"}

func randomVersion(rng *rand.Rand) string {
	if rng.Intn(3) == 0 {
		var b strings.Builder
		for i := 0; i < 1+rng.Intn(8); i++ {
			b.WriteString(versionParts[rng.Intn(len(versionParts))])
		}
		return b.String()
	}
	v := fmt.Sprintf("%d.%d.%d", rng.Intn(3), rng.Intn(12), rng.Intn(12))
	if rng.Intn(2) == 0 {
		v = "v" + v
	}
	if rng.Intn(2) == 0 {
		ids := []string{"rc", "beta", "1", "2", "10", "alpha", "x-1", "011"}
		v += "-" + ids[rng.Intn(len(ids))]
		if rng.Intn(2) == 0 {
			v += "." + ids[rng.Intn(len(ids))]
		}
	}
	if rng.Intn(4) == 0 {
		v += "+build.7"
	}
	return v
}

func writeFirmwareCases(outDir string, rng *rand.Rand, count int) {
	file, err := os.Create(filepath.Join(outDir, "firmware_cases.jsonl"))
	if err != nil {
		panic(err)
	}
	defer file.Close()
	w := bufio.NewWriter(file)
	defer w.Flush()
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for i := 0; i < count; i++ {
		seed := make([]byte, ed25519.SeedSize)
		rng.Read(seed)
		private := ed25519.NewKeyFromSeed(seed)
		manifest := &firmware.FirmwareManifest{
			Version:   randomVersion(rng),
			BuildDate: randomName(rng),
			GitCommit: randomName(rng),
			Files:     map[string]string{},
		}
		for j := 0; j < 1+rng.Intn(12); j++ {
			manifest.Files[randomName(rng)] = hex.EncodeToString(seed[:rng.Intn(32)+1])
		}
		if err := firmware.SignManifest(manifest, private); err != nil {
			panic(err)
		}
		compact, _ := json.Marshal(manifest)
		indented, _ := json.MarshalIndent(manifest, "", "  ")
		if err := enc.Encode(firmwareCase{
			PrivateKey: hex.EncodeToString(private),
			PublicKey:  hex.EncodeToString(private.Public().(ed25519.PublicKey)),
			Manifest:   string(compact),
			Indented:   string(indented),
			Signature:  manifest.Signature,
		}); err != nil {
			panic(err)
		}
	}

	vfile, err := os.Create(filepath.Join(outDir, "version_cases.jsonl"))
	if err != nil {
		panic(err)
	}
	defer vfile.Close()
	vw := bufio.NewWriter(vfile)
	defer vw.Flush()
	venc := json.NewEncoder(vw)
	for i := 0; i < count*4; i++ {
		left, right := randomVersion(rng), randomVersion(rng)
		cmp, err := firmware.CompareReleaseVersions(left, right)
		if err := venc.Encode(versionCase{Left: left, Right: right, Compare: cmp, Error: err != nil, Valid: firmware.IsReleaseVersion(left)}); err != nil {
			panic(err)
		}
	}
	fmt.Fprintf(os.Stderr, "wrote %d firmware and %d version cases to %s\n", count, count*4, outDir)
}
