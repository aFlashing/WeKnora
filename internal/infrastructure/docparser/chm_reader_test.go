package docparser

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
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

func TestCHMEngineIsAvailableWithoutConfiguration(t *testing.T) {
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

func TestCHMReaderRejectsURLImport(t *testing.T) {
	reader := NewCHMReader(&stubDocReader{})
	if _, err := reader.Read(context.Background(), &types.ReadRequest{URL: "https://example.com/a.chm"}); err == nil {
		t.Fatal("expected URL imports to be rejected")
	}
}

func TestCHMReaderNeedsAnEPUBParser(t *testing.T) {
	reader := NewCHMReader(nil)
	_, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("ITSF"), FileName: "manual.chm", FileType: "chm",
	})
	if err == nil || !strings.Contains(err.Error(), "EPUB parser") {
		t.Fatalf("err = %v, want a missing-parser error", err)
	}

	// Without anydoc and without docreader the registry must report the same
	// gap instead of handing back a reader that can only fail later.
	if !anydoc.Available() {
		if _, err := NewReader(context.Background(), CHMEngineName, "chm", false, ReaderDeps{}); err == nil {
			t.Fatal("expected the engine to be unusable without an EPUB parser")
		}
	}
}

func TestCHMReaderReportsConverterFailures(t *testing.T) {
	reader := NewCHMReader(&stubDocReader{result: &types.ReadResult{MarkdownContent: "x"}})
	_, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("not a chm archive"), FileName: "broken.chm", FileType: "chm",
	})
	if err == nil || !strings.Contains(err.Error(), "chm2epub conversion failed") {
		t.Fatalf("err = %v, want a converter failure", err)
	}
}

// A rule saved while chm shared the EPUB group names anydoc, which cannot read
// the container. The registry must route such a file to the engine that does.
func TestStaleRuleForCHMFallsBackToTheCHMEngine(t *testing.T) {
	reader, err := NewReader(context.Background(), AnydocEngineName, "chm", false, ReaderDeps{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reader.(*CHMReader); !ok {
		t.Fatalf("NewReader(anydoc, chm) = %T, want *CHMReader", reader)
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

// TestCHMConversionEndToEnd runs the real pipeline when an archive is supplied;
// it is skipped by default so CI stays hermetic.
func TestCHMConversionEndToEnd(t *testing.T) {
	input := os.Getenv("WEKNORA_TEST_CHM_FILE")
	if input == "" {
		t.Skip("set WEKNORA_TEST_CHM_FILE to a .chm file to run")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	// Go through the registry, exactly as the ingest pipeline does, so this
	// also covers engine availability and the fallback selection.
	reader, err := NewReader(context.Background(), CHMEngineName, "chm", false, ReaderDeps{})
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
