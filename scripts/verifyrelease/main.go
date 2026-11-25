package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

var requiredTargets = []string{
	"darwin-arm64",
	"darwin-amd64",
	"windows-amd64",
}

const buildMetaFileName = ".build-meta.json"

type mygoConfig struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Resources []string `json:"resources"`
	Updates   struct {
		PublicKey string `json:"publicKey"`
		GitHub    string `json:"github"`
	} `json:"updates"`
}

type updateManifest struct {
	Version   string        `json:"version"`
	URL       string        `json:"url"`
	Size      int64         `json:"size"`
	Signature string        `json:"signature"`
	Deltas    []updateDelta `json:"deltas"`
}

type updateDelta struct {
	From      string `json:"from"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	Signature string `json:"signature"`
}

type buildMetadata struct {
	Version         string `json:"version"`
	Commit          string `json:"commit"`
	Dirty           bool   `json:"dirty"`
	ChecksumsSHA256 string `json:"checksums_sha256"`
}

type verifyOptions struct {
	Dir                 string
	ConfigPath          string
	Version             string
	ExpectedCommit      string
	RequireWinInstaller bool
}

func main() {
	var opts verifyOptions
	flag.StringVar(&opts.Dir, "dir", "release", "release artifacts directory")
	flag.StringVar(&opts.ConfigPath, "config", "cmd/xylring-main/mygo.json", "path to mygo.json")
	flag.StringVar(&opts.Version, "version", "", "expected version (defaults to mygo.json version)")
	flag.StringVar(&opts.ExpectedCommit, "expected-commit", "", "expected git commit recorded in .build-meta.json")
	flag.BoolVar(&opts.RequireWinInstaller, "require-win-installer", true, "require Windows NSIS setup installer")
	flag.Parse()

	if err := verifyRelease(opts); err != nil {
		fmt.Fprintf(os.Stderr, "release verification failed: %v\n", err)
		os.Exit(1)
	}
}

func verifyRelease(opts verifyOptions) error {
	cfgBytes, err := os.ReadFile(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("read config %s: %w", opts.ConfigPath, err)
	}
	var cfg mygoConfig
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		return fmt.Errorf("parse config %s: %w", opts.ConfigPath, err)
	}
	cfgVersion := strings.TrimSpace(cfg.Version)
	if cfgVersion == "" {
		return fmt.Errorf("config %s has empty version", opts.ConfigPath)
	}
	expectedVersion := strings.TrimSpace(opts.Version)
	if expectedVersion == "" {
		expectedVersion = cfgVersion
	} else if expectedVersion != cfgVersion {
		return fmt.Errorf("expected version %q does not match mygo.json version %q", expectedVersion, cfgVersion)
	}
	appPrefix := strings.ToLower(strings.TrimSpace(cfg.Name))
	if appPrefix == "" {
		appPrefix = "xylring"
	}
	winExeName := strings.TrimSpace(cfg.Name)
	if winExeName == "" {
		winExeName = "Xylring"
	}
	winExeName += ".exe"

	pubKeyBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(cfg.Updates.PublicKey))
	if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid updates.publicKey in %s", opts.ConfigPath)
	}
	pubKey := ed25519.PublicKey(pubKeyBytes)

	entries, err := os.ReadDir(opts.Dir)
	if err != nil {
		return fmt.Errorf("read release dir %s: %w", opts.Dir, err)
	}
	actualFiles := make(map[string]struct{}, len(entries))
	hasBuildMeta := false
	for _, entry := range entries {
		if entry.IsDir() {
			return fmt.Errorf("unexpected subdirectory in release dir: %s", entry.Name())
		}
		if entry.Name() == buildMetaFileName {
			hasBuildMeta = true
			continue
		}
		actualFiles[entry.Name()] = struct{}{}
	}

	expectedFiles := map[string]struct{}{}

	// 1. Verify per-platform installers / portable packages.
	checkSingleMatch := func(desc string, match func(name string) bool) (string, error) {
		var matched []string
		for name := range actualFiles {
			if match(name) {
				matched = append(matched, name)
			}
		}
		if len(matched) == 0 {
			return "", fmt.Errorf("missing %s for version %s", desc, expectedVersion)
		}
		if len(matched) > 1 {
			slices.Sort(matched)
			return "", fmt.Errorf("multiple %s found: %v", desc, matched)
		}
		info, err := os.Stat(filepath.Join(opts.Dir, matched[0]))
		if err != nil || info.Size() <= 0 {
			return "", fmt.Errorf("invalid %s %s: empty or unreadable", desc, matched[0])
		}
		expectedFiles[matched[0]] = struct{}{}
		return matched[0], nil
	}

	if _, err := checkSingleMatch("macOS arm64 DMG", func(name string) bool {
		return name == fmt.Sprintf("%s-%s-arm64.dmg", appPrefix, expectedVersion)
	}); err != nil {
		return err
	}
	if _, err := checkSingleMatch("macOS amd64 DMG", func(name string) bool {
		return name == fmt.Sprintf("%s-%s-amd64.dmg", appPrefix, expectedVersion)
	}); err != nil {
		return err
	}
	winZip := fmt.Sprintf("%s-%s-windows-x64.zip", appPrefix, expectedVersion)
	if _, err := checkSingleMatch("Windows portable zip", func(name string) bool {
		return name == winZip
	}); err != nil {
		return err
	}
	if err := verifyWindowsPortableZip(filepath.Join(opts.Dir, winZip), winExeName, cfg.Resources); err != nil {
		return fmt.Errorf("windows portable zip %s: %w", winZip, err)
	}
	winSetup := fmt.Sprintf("%s-setup-%s-amd64.exe", appPrefix, expectedVersion)
	if opts.RequireWinInstaller {
		if _, err := checkSingleMatch("Windows setup installer", func(name string) bool {
			return name == winSetup
		}); err != nil {
			return err
		}
	} else {
		if _, ok := actualFiles[winSetup]; ok {
			expectedFiles[winSetup] = struct{}{}
		}
	}

	// 2. Verify update manifests, full archives, deltas, sizes, and Ed25519 signatures.
	tag := "v" + expectedVersion
	for _, target := range requiredTargets {
		manifestName := fmt.Sprintf("update-%s.json", target)
		if _, ok := actualFiles[manifestName]; !ok {
			return fmt.Errorf("missing update manifest %s", manifestName)
		}
		expectedFiles[manifestName] = struct{}{}

		raw, err := os.ReadFile(filepath.Join(opts.Dir, manifestName))
		if err != nil {
			return fmt.Errorf("read %s: %w", manifestName, err)
		}
		var m updateManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("parse %s: %w", manifestName, err)
		}
		if m.Version != expectedVersion {
			return fmt.Errorf("%s version is %q, expected %q", manifestName, m.Version, expectedVersion)
		}

		expectedArchive := fmt.Sprintf("%s-%s-%s.tar.gz", appPrefix, expectedVersion, target)
		if err := verifyDownloadURL(m.URL, cfg.Updates.GitHub, tag, expectedArchive); err != nil {
			return fmt.Errorf("%s archive url: %w", manifestName, err)
		}
		if err := verifySignedArtifact(filepath.Join(opts.Dir, expectedArchive), m.Size, m.Signature, pubKey); err != nil {
			return fmt.Errorf("%s full archive %s: %w", manifestName, expectedArchive, err)
		}
		expectedFiles[expectedArchive] = struct{}{}

		seenFrom := make(map[string]struct{}, len(m.Deltas))
		for _, d := range m.Deltas {
			from := strings.TrimSpace(d.From)
			if from == "" || from == expectedVersion || strings.ContainsAny(from, `/\ `) || strings.Contains(from, "..") {
				return fmt.Errorf("%s delta has invalid from version %q", manifestName, d.From)
			}
			if _, dup := seenFrom[from]; dup {
				return fmt.Errorf("%s has duplicate delta for from version %q", manifestName, from)
			}
			seenFrom[from] = struct{}{}

			expectedDelta := fmt.Sprintf("%s-%s-to-%s-%s.delta", appPrefix, from, expectedVersion, target)
			if err := verifyDownloadURL(d.URL, cfg.Updates.GitHub, tag, expectedDelta); err != nil {
				return fmt.Errorf("%s delta (from %s) url: %w", manifestName, from, err)
			}
			if err := verifySignedArtifact(filepath.Join(opts.Dir, expectedDelta), d.Size, d.Signature, pubKey); err != nil {
				return fmt.Errorf("%s delta %s: %w", manifestName, expectedDelta, err)
			}
			expectedFiles[expectedDelta] = struct{}{}
		}
	}

	// 3. Reject any stale/unreferenced files in the release directory.
	for name := range actualFiles {
		if name == "checksums.txt" {
			continue
		}
		if _, ok := expectedFiles[name]; !ok {
			return fmt.Errorf("unexpected or stale file in release dir: %s", name)
		}
	}

	// 4. Verify checksums.txt covers every artifact and every SHA-256 matches.
	if err := verifyChecksums(opts.Dir, expectedFiles); err != nil {
		return err
	}

	// 5. Verify build metadata when present or when expected-commit is specified.
	expectedCommit := strings.TrimSpace(opts.ExpectedCommit)
	if expectedCommit != "" || hasBuildMeta {
		if err := verifyBuildMetadata(opts.Dir, expectedVersion, expectedCommit); err != nil {
			return err
		}
	}
	return nil
}

func verifyWindowsPortableZip(zipPath, exeName string, resources []string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	entries := make(map[string]*zip.File, len(zr.File))
	var exePaths []string
	for _, f := range zr.File {
		raw := strings.ReplaceAll(f.Name, "\\", "/")
		if raw == "" || strings.HasPrefix(raw, "/") || (len(raw) >= 2 && raw[1] == ':') {
			return fmt.Errorf("invalid path inside portable zip: %s", f.Name)
		}
		trimmed := strings.TrimSuffix(raw, "/")
		if trimmed == "" {
			if f.FileInfo().IsDir() {
				continue
			}
			return fmt.Errorf("invalid empty path inside portable zip: %s", f.Name)
		}
		for _, seg := range strings.Split(trimmed, "/") {
			if seg == "" || seg == "." || seg == ".." {
				return fmt.Errorf("invalid path segment inside portable zip: %s", f.Name)
			}
		}
		if f.FileInfo().IsDir() {
			continue
		}
		clean := path.Clean(trimmed)
		if _, dup := entries[clean]; dup {
			return fmt.Errorf("duplicate entry inside portable zip: %s", clean)
		}
		base := path.Base(clean)
		lower := strings.ToLower(base)
		if strings.Contains(lower, "setup") && strings.HasSuffix(lower, ".exe") {
			return fmt.Errorf("unexpected installer inside portable zip: %s", f.Name)
		}
		if strings.HasSuffix(lower, ".delta") || strings.HasSuffix(lower, ".tar.gz") || (strings.HasPrefix(lower, "update-") && strings.HasSuffix(lower, ".json")) {
			return fmt.Errorf("unexpected updater artifact inside portable zip: %s", f.Name)
		}
		entries[clean] = f
		if base == exeName {
			exePaths = append(exePaths, clean)
		}
	}

	if len(exePaths) == 0 {
		return fmt.Errorf("missing %s in portable zip", exeName)
	}
	if len(exePaths) > 1 {
		slices.Sort(exePaths)
		return fmt.Errorf("multiple %s entries in portable zip: %v", exeName, exePaths)
	}
	exePath := exePaths[0]
	exeDir := path.Dir(exePath)
	if exeDir != "." && strings.Contains(exeDir, "/") {
		return fmt.Errorf("invalid layout: %s is nested too deeply at %s", exeName, exePath)
	}
	if exeDir != "." {
		prefix := exeDir + "/"
		for p := range entries {
			if !strings.HasPrefix(p, prefix) {
				return fmt.Errorf("invalid layout: entry %s is outside top-level directory %s", p, exeDir)
			}
		}
	}

	readAndVerify := func(relPath, label string) error {
		zf, ok := entries[relPath]
		if !ok {
			return fmt.Errorf("missing %s at expected path %s in portable zip", label, relPath)
		}
		rc, err := zf.Open()
		if err != nil {
			return fmt.Errorf("open %s (%s): %w", label, relPath, err)
		}
		n, copyErr := io.Copy(io.Discard, rc)
		closeErr := rc.Close()
		if copyErr != nil {
			return fmt.Errorf("extract %s (%s): %w", label, relPath, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s (%s): %w", label, relPath, closeErr)
		}
		if n <= 0 {
			return fmt.Errorf("empty %s (%s) in portable zip", label, relPath)
		}
		if uint64(n) != zf.UncompressedSize64 {
			return fmt.Errorf("%s (%s) decompressed size %d does not match header size %d", label, relPath, n, zf.UncompressedSize64)
		}
		return nil
	}

	if err := readAndVerify(exePath, exeName); err != nil {
		return err
	}
	for _, res := range resources {
		base := filepath.Base(strings.TrimSpace(res))
		if base == "" || base == "." || base == string(filepath.Separator) {
			continue
		}
		wantPath := base
		if exeDir != "." {
			wantPath = path.Join(exeDir, base)
		}
		if err := readAndVerify(wantPath, "resource "+base); err != nil {
			return err
		}
	}
	return nil
}

func verifyBuildMetadata(dir, expectedVersion, expectedCommit string) error {
	metaPath := filepath.Join(dir, buildMetaFileName)
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return fmt.Errorf("read %s: %w", buildMetaFileName, err)
	}
	var meta buildMetadata
	if err := json.Unmarshal(raw, &meta); err != nil {
		return fmt.Errorf("parse %s: %w", buildMetaFileName, err)
	}
	if strings.TrimSpace(meta.Version) != expectedVersion {
		return fmt.Errorf("%s version is %q, expected %q", buildMetaFileName, meta.Version, expectedVersion)
	}
	if strings.TrimSpace(meta.Commit) == "" {
		return fmt.Errorf("%s has empty commit", buildMetaFileName)
	}
	if meta.Dirty {
		return fmt.Errorf("%s indicates artifacts were built from a dirty worktree", buildMetaFileName)
	}
	if expectedCommit != "" && !strings.EqualFold(strings.TrimSpace(meta.Commit), expectedCommit) {
		return fmt.Errorf("%s commit is %q, expected %q", buildMetaFileName, meta.Commit, expectedCommit)
	}
	checksumsDigest, err := fileSHA256(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("hash checksums.txt: %w", err)
	}
	gotChecksumsHex := hex.EncodeToString(checksumsDigest)
	if !strings.EqualFold(strings.TrimSpace(meta.ChecksumsSHA256), gotChecksumsHex) {
		return fmt.Errorf("%s checksums_sha256 mismatch (got %s, meta has %s)", buildMetaFileName, gotChecksumsHex, meta.ChecksumsSHA256)
	}
	return nil
}

func verifyDownloadURL(rawURL, githubRepo, tag, expectedFile string) error {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("invalid url %q", rawURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("url %q must not contain query or fragment", rawURL)
	}
	wantPath := "/releases/download/" + tag + "/" + expectedFile
	if repo := strings.Trim(strings.TrimSpace(githubRepo), "/"); repo != "" {
		if !strings.EqualFold(u.Host, "github.com") {
			return fmt.Errorf("url %q host is %q, expected github.com", rawURL, u.Host)
		}
		wantPath = "/" + repo + wantPath
	}
	if u.Path != wantPath {
		return fmt.Errorf("url %q path is %q, expected %q", rawURL, u.Path, wantPath)
	}
	return nil
}

func verifySignedArtifact(filePath string, expectedSize int64, signature string, pubKey ed25519.PublicKey) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}
	if expectedSize <= 0 || info.Size() != expectedSize {
		return fmt.Errorf("size mismatch: got %d, manifest has %d", info.Size(), expectedSize)
	}
	if strings.TrimSpace(signature) == "" {
		return fmt.Errorf("missing signature")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil {
		return fmt.Errorf("invalid base64 signature: %w", err)
	}
	digest, err := fileSHA256(filePath)
	if err != nil {
		return err
	}
	if !ed25519.Verify(pubKey, digest, sig) {
		return fmt.Errorf("ed25519 signature verification failed")
	}
	return nil
}

func fileSHA256(filePath string) ([]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func verifyChecksums(dir string, expectedFiles map[string]struct{}) error {
	raw, err := os.ReadFile(filepath.Join(dir, "checksums.txt"))
	if err != nil {
		return fmt.Errorf("read checksums.txt: %w", err)
	}
	seen := make(map[string]struct{}, len(expectedFiles))
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			return fmt.Errorf("checksums.txt:%d: malformed line %q", i+1, line)
		}
		wantHex := strings.ToLower(fields[0])
		name := strings.TrimPrefix(fields[1], "*")
		if _, ok := expectedFiles[name]; !ok {
			return fmt.Errorf("checksums.txt:%d: unexpected file %q", i+1, name)
		}
		if _, dup := seen[name]; dup {
			return fmt.Errorf("checksums.txt:%d: duplicate entry %q", i+1, name)
		}
		seen[name] = struct{}{}
		digest, err := fileSHA256(filepath.Join(dir, name))
		if err != nil {
			return fmt.Errorf("checksums.txt: hash %s: %w", name, err)
		}
		gotHex := hex.EncodeToString(digest)
		if gotHex != wantHex {
			return fmt.Errorf("checksums.txt: %s sha256 mismatch (got %s, want %s)", name, gotHex, wantHex)
		}
	}
	for name := range expectedFiles {
		if _, ok := seen[name]; !ok {
			return fmt.Errorf("checksums.txt missing entry for %s", name)
		}
	}
	return nil
}
