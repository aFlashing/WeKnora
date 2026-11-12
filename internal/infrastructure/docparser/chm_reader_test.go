package docparser

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

func TestCHMIsAnImportableFileType(t *testing.T) {
	if !utils.IsSupportedImportExtension("chm") {
		t.Fatal("chm must be accepted by every import path")
	}
}

func TestCHMRoutesToItsOwnEngineByDefault(t *testing.T) {
	for _, input := range []string{"chm", ".CHM", " Chm "} {
		if got := types.DefaultParserEngine(input); got != CHMEngineName {
			t.Fatalf("DefaultParserEngine(%q) = %q, want %q", input, got, CHMEngineName)
		}
	}
}

func TestCHMAvailabilityFollowsTheConfiguredPath(t *testing.T) {
	t.Setenv(chmConvertPathEnv, "")

	if ok, reason := checkCHMConverter(nil); ok || !strings.Contains(reason, "not configured") {
		t.Fatalf("unconfigured engine: ok=%v reason=%q", ok, reason)
	}

	dir := t.TempDir()
	exe := filepath.Join(dir, "ebook-convert.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if ok, reason := checkCHMConverter(map[string]string{chmConvertPathOverride: exe}); !ok {
		t.Fatalf("configured engine reported unavailable: %s", reason)
	}

	missing := filepath.Join(dir, "nope.exe")
	if ok, reason := checkCHMConverter(map[string]string{chmConvertPathOverride: missing}); ok ||
		!strings.Contains(reason, "not found") {
		t.Fatalf("missing executable: ok=%v reason=%q", ok, reason)
	}

	if ok, reason := checkCHMConverter(map[string]string{chmConvertPathOverride: dir}); ok ||
		!strings.Contains(reason, "directory") {
		t.Fatalf("directory path: ok=%v reason=%q", ok, reason)
	}
}

func TestCHMEngineIsListedWhenTheEnvironmentConfiguresIt(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "ebook-convert.exe")
	if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(chmConvertPathEnv, exe)

	var found *types.ParserEngineInfo
	for _, engine := range ListAllEngines(false, nil, nil) {
		if engine.Name == CHMEngineName {
			e := engine
			found = &e
		}
	}
	if found == nil {
		t.Fatal("chm engine missing from the engine list")
	}
	if !found.Available {
		t.Fatalf("chm engine unavailable: %s", found.UnavailableReason)
	}
	if len(found.FileTypes) != 1 || found.FileTypes[0] != "chm" {
		t.Fatalf("file types = %v", found.FileTypes)
	}
}

func TestCHMConvertPathPrefersTheTenantSetting(t *testing.T) {
	t.Setenv(chmConvertPathEnv, "/from/env")
	if got := ResolveCHMConvertPath(nil); got != "/from/env" {
		t.Fatalf("env fallback = %q", got)
	}
	if got := ResolveCHMConvertPath(map[string]string{chmConvertPathOverride: " /from/tenant "}); got != "/from/tenant" {
		t.Fatalf("tenant setting = %q", got)
	}
}

func TestCHMEngineReaderRequiresAConverter(t *testing.T) {
	t.Setenv(chmConvertPathEnv, "")
	if _, err := NewReader(
		context.Background(), CHMEngineName, "chm", false, ReaderDeps{},
	); err == nil {
		t.Fatal("expected the engine to report itself unusable without a converter")
	}
}

func TestCHMReaderRejectsURLImport(t *testing.T) {
	reader := NewCHMReader(
		map[string]string{chmConvertPathOverride: fakeConverter(t, "")},
		&stubDocReader{},
	)
	if _, err := reader.Read(context.Background(), &types.ReadRequest{URL: "https://example.com/a.chm"}); err == nil {
		t.Fatal("expected URL imports to be rejected")
	}
}

func TestCHMReaderNeedsAnEPUBParser(t *testing.T) {
	reader := NewCHMReader(map[string]string{chmConvertPathOverride: fakeConverter(t, "")}, nil)
	_, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("ITSF"), FileName: "manual.chm", FileType: "chm",
	})
	if err == nil || !strings.Contains(err.Error(), "EPUB parser") {
		t.Fatalf("err = %v, want a missing-parser error", err)
	}
}

func TestCHMReaderParsesTheConvertedEPUB(t *testing.T) {
	fallback := &stubDocReader{result: &types.ReadResult{
		MarkdownContent: "# 第 1 章\n\n正文",
		Metadata:        map[string]string{"parser": "anydoc"},
	}}
	reader := NewCHMReader(map[string]string{chmConvertPathOverride: fakeConverter(t, "")}, fallback)

	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("ITSF"), FileName: "CODESYS.chm", FileType: "chm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata["parser"] != CHMEngineName {
		t.Fatalf("parser = %q", result.Metadata["parser"])
	}
	if result.Metadata["chm_epub_parser"] != "anydoc" {
		t.Fatalf("chm_epub_parser = %q", result.Metadata["chm_epub_parser"])
	}
	if fallback.got == nil {
		t.Fatal("the converted EPUB never reached the fallback reader")
	}
	if fallback.got.FileType != "epub" || fallback.got.FileName != "CODESYS.epub" {
		t.Fatalf("fallback got %q/%q, want epub/CODESYS.epub", fallback.got.FileType, fallback.got.FileName)
	}
	if !strings.HasPrefix(string(fallback.got.FileContent), "PK") {
		t.Fatal("fallback did not receive the converter output")
	}
}

func TestCHMReaderConverterFailures(t *testing.T) {
	for _, mode := range []string{"fail", "garbage", "empty"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("WEKNORA_FAKE_CONVERTER_MODE", mode)
			reader := NewCHMReader(
				map[string]string{chmConvertPathOverride: fakeConverter(t, "")},
				&stubDocReader{result: &types.ReadResult{MarkdownContent: "x"}},
			)
			_, err := reader.Read(context.Background(), &types.ReadRequest{
				FileContent: []byte("ITSF"), FileName: "manual.chm", FileType: "chm",
			})
			if err == nil {
				t.Fatalf("mode %q: expected an error", mode)
			}
			want := map[string]string{
				"fail":    "ebook-convert failed",
				"garbage": "not an EPUB",
				"empty":   "empty EPUB",
			}[mode]
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("mode %q: err = %v, want %q", mode, err, want)
			}
		})
	}
}

func TestCHMReaderTimesOut(t *testing.T) {
	t.Setenv("WEKNORA_FAKE_CONVERTER_MODE", "slow")
	t.Setenv(chmConvertTimeoutEnv, "500ms")
	reader := NewCHMReader(
		map[string]string{chmConvertPathOverride: fakeConverter(t, "")},
		&stubDocReader{result: &types.ReadResult{MarkdownContent: "x"}},
	)
	_, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("ITSF"), FileName: "manual.chm", FileType: "chm",
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout", err)
	}
}

func TestCHMEPUBFileName(t *testing.T) {
	for input, want := range map[string]string{
		"manual.chm":  "manual.epub",
		"CODESYS.CHM": "CODESYS.epub",
		"无后缀":         "无后缀.epub",
		"a.b.chm":     "a.b.epub",
	} {
		if got := chmEPUBFileName(input); got != want {
			t.Errorf("chmEPUBFileName(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestCHMConversionWithCalibre runs the real pipeline when a converter and an
// archive are supplied; it is skipped by default so CI stays hermetic.
func TestCHMConversionWithCalibre(t *testing.T) {
	exe := os.Getenv("WEKNORA_TEST_EBOOK_CONVERT")
	input := os.Getenv("WEKNORA_TEST_CHM_FILE")
	if exe == "" || input == "" {
		t.Skip("set WEKNORA_TEST_EBOOK_CONVERT and WEKNORA_TEST_CHM_FILE to run")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	// Go through the registry, exactly as the ingest pipeline does, so this
	// also covers engine availability and the fallback selection.
	reader, err := NewReader(context.Background(), CHMEngineName, "chm", false, ReaderDeps{
		Overrides: map[string]string{chmConvertPathOverride: exe},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: data, FileName: filepath.Base(input), FileType: "chm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.MarkdownContent) == "" {
		t.Fatal("converted EPUB produced no markdown")
	}
	if result.Metadata["parser"] != CHMEngineName {
		t.Fatalf("metadata parser = %q", result.Metadata["parser"])
	}
	t.Logf("converted %s to %d bytes of markdown with %d images",
		filepath.Base(input), len(result.MarkdownContent), len(result.ImageRefs))
}

// fakeConverter builds a tiny executable that mimics ebook-convert: it writes
// the archive named by its second argument, or misbehaves according to
// WEKNORA_FAKE_CONVERTER_MODE. Building it keeps the subprocess path under test
// without requiring Calibre.
func fakeConverter(t *testing.T, _ string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; cannot build the fake converter")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeConverterSource), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "fake-convert"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	exe := filepath.Join(dir, name)
	if out, err := exec.Command("go", "build", "-o", exe, src).CombinedOutput(); err != nil {
		t.Fatalf("build fake converter: %v: %s", err, out)
	}
	return exe
}

const fakeConverterSource = `package main

import (
	"os"
	"time"
)

func main() {
	if len(os.Args) < 3 {
		os.Exit(2)
	}
	switch os.Getenv("WEKNORA_FAKE_CONVERTER_MODE") {
	case "fail":
		os.Exit(3)
	case "slow":
		time.Sleep(30 * time.Second)
	case "garbage":
		_ = os.WriteFile(os.Args[2], []byte("not a zip"), 0o644)
	case "empty":
		_ = os.WriteFile(os.Args[2], nil, 0o644)
	default:
		// A minimal empty ZIP header; the CHM reader only checks the magic.
		_ = os.WriteFile(os.Args[2], []byte("PK\x05\x06\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"), 0o644)
	}
}
`
