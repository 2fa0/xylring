package main

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func makeZipBytes(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(entries[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildValidTestRelease(t *testing.T, version string) (string, string, ed25519.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(t.TempDir(), "mygo.json")
	cfg := mygoConfig{
		Name:      "Xylring",
		Version:   version,
		Resources: []string{"../../LICENSE", "../../NOTICE"},
	}
	cfg.Updates.PublicKey = base64.StdEncoding.EncodeToString(pub)
	cfg.Updates.GitHub = "2fa0/xylring"
	cfgRaw, _ := json.Marshal(cfg)
	if err := os.WriteFile(cfgPath, cfgRaw, 0o600); err != nil {
		t.Fatal(err)
	}

	writeBytes := func(name string, content []byte) (int64, string) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(content)
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, sum[:]))
		return int64(len(content)), sig
	}
	writeFile := func(name, content string) (int64, string) {
		return writeBytes(name, []byte(content))
	}

	writeFile(fmt.Sprintf("xylring-%s-arm64.dmg", version), "arm64-dmg")
	writeFile(fmt.Sprintf("xylring-%s-amd64.dmg", version), "amd64-dmg")
	writeBytes(fmt.Sprintf("xylring-%s-windows-x64.zip", version), makeZipBytes(t, map[string]string{
		"Xylring.exe": "MZ-fake-exe",
		"LICENSE":     "MIT",
		"NOTICE":      "notice",
	}))
	writeFile(fmt.Sprintf("xylring-setup-%s-amd64.exe", version), "win-setup")

	for _, target := range requiredTargets {
		archName := fmt.Sprintf("xylring-%s-%s.tar.gz", version, target)
		archSize, archSig := writeFile(archName, "archive-"+target)

		deltaName := fmt.Sprintf("xylring-0.10.0-to-%s-%s.delta", version, target)
		deltaSize, deltaSig := writeFile(deltaName, "delta-"+target)

		m := updateManifest{
			Version:   version,
			URL:       fmt.Sprintf("https://github.com/2fa0/xylring/releases/download/v%s/%s", version, archName),
			Size:      archSize,
			Signature: archSig,
			Deltas: []updateDelta{
				{
					From:      "0.10.0",
					URL:       fmt.Sprintf("https://github.com/2fa0/xylring/releases/download/v%s/%s", version, deltaName),
					Size:      deltaSize,
					Signature: deltaSig,
				},
			},
		}
		raw, _ := json.MarshalIndent(m, "", "  ")
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("update-%s.json", target)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writeChecksums(t, dir)
	writeBuildMeta(t, dir, version, "deadbeef1234567890", false)
	return dir, cfgPath, priv
}

func writeChecksums(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.Name() == "checksums.txt" || e.Name() == buildMetaFileName {
			continue
		}
		names = append(names, e.Name())
	}
	slices.Sort(names)
	var b strings.Builder
	for _, name := range names {
		digest, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&b, "%s  %s\n", hex.EncodeToString(digest), name)
	}
	if err := os.WriteFile(filepath.Join(dir, "checksums.txt"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeBuildMeta(t *testing.T, dir, version, commit string, dirty bool) {
	t.Helper()
	digest, err := fileSHA256(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.MarshalIndent(buildMetadata{
		Version:         version,
		Commit:          commit,
		Dirty:           dirty,
		ChecksumsSHA256: hex.EncodeToString(digest),
	}, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, buildMetaFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyReleaseValid(t *testing.T) {
	dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
	if err := verifyRelease(verifyOptions{
		Dir:                 dir,
		ConfigPath:          cfgPath,
		Version:             "0.11.0",
		ExpectedCommit:      "deadbeef1234567890",
		RequireWinInstaller: true,
	}); err != nil {
		t.Fatalf("expected valid release, got error: %v", err)
	}
}

func TestVerifyReleaseRejectsMissingPlatformOrFullArchiveOrBadSig(t *testing.T) {
	t.Run("missing intel dmg", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		_ = os.Remove(filepath.Join(dir, "xylring-0.11.0-amd64.dmg"))
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil {
			t.Fatal("expected error for missing amd64 dmg")
		}
	})

	t.Run("missing full archive", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		_ = os.Remove(filepath.Join(dir, "xylring-0.11.0-windows-amd64.tar.gz"))
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil {
			t.Fatal("expected error for missing full archive")
		}
	})

	t.Run("tampered archive fails signature", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		p := filepath.Join(dir, "xylring-0.11.0-darwin-arm64.tar.gz")
		_ = os.WriteFile(p, []byte("ARCHIVE-darwin-arm64"), 0o600) // same length, different bytes
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "signature") {
			t.Fatalf("expected signature error, got: %v", err)
		}
	})

	t.Run("stale old version file rejected", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		_ = os.WriteFile(filepath.Join(dir, "xylring-0.10.0-arm64.dmg"), []byte("old"), 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "stale") {
			t.Fatalf("expected stale file error, got: %v", err)
		}
	})

	t.Run("checksum mismatch rejected", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		p := filepath.Join(dir, "xylring-0.11.0-arm64.dmg")
		_ = os.WriteFile(p, []byte("modified-"), 0o600)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "checksums.txt") {
			t.Fatalf("expected checksum mismatch error, got: %v", err)
		}
	})
}

func TestVerifyReleaseStrictURLDeltaZipAndBuildMeta(t *testing.T) {
	t.Run("rejects wrong download host or extra path segment", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		mp := filepath.Join(dir, "update-darwin-arm64.json")
		raw, _ := os.ReadFile(mp)
		var m updateManifest
		_ = json.Unmarshal(raw, &m)
		m.URL = "https://evil.example.com/2fa0/xylring/releases/download/v0.11.0/xylring-0.11.0-darwin-arm64.tar.gz"
		updated, _ := json.MarshalIndent(m, "", "  ")
		_ = os.WriteFile(mp, updated, 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "github.com") {
			t.Fatalf("expected host error, got: %v", err)
		}
	})

	t.Run("rejects delta with mismatched platform or from version", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		mp := filepath.Join(dir, "update-darwin-arm64.json")
		raw, _ := os.ReadFile(mp)
		var m updateManifest
		_ = json.Unmarshal(raw, &m)
		m.Deltas[0].From = "0.9.2"
		updated, _ := json.MarshalIndent(m, "", "  ")
		_ = os.WriteFile(mp, updated, 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "delta") {
			t.Fatalf("expected delta binding error, got: %v", err)
		}
	})

	t.Run("rejects windows portable zip missing exe or resource", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		zipPath := filepath.Join(dir, "xylring-0.11.0-windows-x64.zip")
		_ = os.WriteFile(zipPath, makeZipBytes(t, map[string]string{
			"Xylring.exe": "MZ-fake-exe",
			"LICENSE":     "MIT",
			// NOTICE missing
		}), 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "NOTICE") {
			t.Fatalf("expected missing NOTICE error, got: %v", err)
		}
	})

	t.Run("rejects build metadata commit mismatch or dirty build", func(t *testing.T) {
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		if err := verifyRelease(verifyOptions{
			Dir:                 dir,
			ConfigPath:          cfgPath,
			Version:             "0.11.0",
			ExpectedCommit:      "111111111111111111",
			RequireWinInstaller: true,
		}); err == nil || !strings.Contains(err.Error(), "commit") {
			t.Fatalf("expected commit mismatch error, got: %v", err)
		}

		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", true)
		if err := verifyRelease(verifyOptions{
			Dir:                 dir,
			ConfigPath:          cfgPath,
			Version:             "0.11.0",
			RequireWinInstaller: true,
		}); err == nil || !strings.Contains(err.Error(), "dirty") {
			t.Fatalf("expected dirty build error even without expected-commit, got: %v", err)
		}
	})

	t.Run("rejects misplaced or corrupted entries in windows portable zip", func(t *testing.T) {
		// Resource in a different directory than Xylring.exe must fail.
		dir, cfgPath, _ := buildValidTestRelease(t, "0.11.0")
		zipPath := filepath.Join(dir, "xylring-0.11.0-windows-x64.zip")
		_ = os.WriteFile(zipPath, makeZipBytes(t, map[string]string{
			"Xylring.exe":    "MZ-fake-exe",
			"LICENSE":        "MIT",
			"nested/NOTICE":  "notice",
		}), 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "NOTICE") {
			t.Fatalf("expected misplaced resource error, got: %v", err)
		}

		// Deeply nested executable must fail.
		_ = os.WriteFile(zipPath, makeZipBytes(t, map[string]string{
			"a/b/Xylring.exe": "MZ-fake-exe",
			"a/b/LICENSE":     "MIT",
			"a/b/NOTICE":      "notice",
		}), 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "nested") {
			t.Fatalf("expected deeply nested layout error, got: %v", err)
		}

		// Single top-level folder with exe and resources alongside it is valid.
		_ = os.WriteFile(zipPath, makeZipBytes(t, map[string]string{
			"Xylring/Xylring.exe": "MZ-fake-exe",
			"Xylring/LICENSE":     "MIT",
			"Xylring/NOTICE":      "notice",
		}), 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err != nil {
			t.Fatalf("expected single top-level dir layout to pass, got: %v", err)
		}

		// Corrupted entry payload (CRC mismatch) must fail during full read.
		rawZip := makeZipBytes(t, map[string]string{
			"Xylring.exe": "MZ-fake-exe-payload",
			"LICENSE":     "MIT",
			"NOTICE":      "notice",
		})
		idx := bytes.Index(rawZip, []byte("MZ-fake-exe-payload"))
		if idx < 0 {
			t.Fatal("failed to locate payload in test zip")
		}
		rawZip[idx] ^= 0xff
		_ = os.WriteFile(zipPath, rawZip, 0o600)
		writeChecksums(t, dir)
		writeBuildMeta(t, dir, "0.11.0", "deadbeef1234567890", false)
		if err := verifyRelease(verifyOptions{Dir: dir, ConfigPath: cfgPath, RequireWinInstaller: true}); err == nil || !strings.Contains(err.Error(), "extract") {
			t.Fatalf("expected corrupt zip extract error, got: %v", err)
		}
	})
}
