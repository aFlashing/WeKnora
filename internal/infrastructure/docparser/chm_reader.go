package docparser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	chm2epub "github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/convert"
	"github.com/Tencent/WeKnora/internal/infrastructure/chm2epub/logx"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// CHMEngineName is the in-process CHM engine.
	CHMEngineName = "chm"
	// chmConvertedFileType is what the converter emits. EPUB is the format both
	// the anydoc binding and docreader parse.
	chmConvertedFileType = "epub"
)

// CHMReader decompresses a compiled HTML Help archive into an EPUB and hands the
// EPUB to whichever engine parses EPUBs here: the in-process anydoc binding, or
// the docreader service when this build does not link it.
//
// CHM is a container (LZX-compressed HTML plus a .hhc topic index), not a
// document format, so no parser reads it directly. The converter is pure Go
// (internal/infrastructure/chm2epub): no Calibre, no subprocess, no cgo.
type CHMReader struct {
	// fallback turns the intermediate EPUB into markdown. Nil means the
	// required EPUB parser is missing and Read reports that.
	fallback interfaces.DocReader
}

// NewCHMReader builds a reader around the EPUB parser this deployment has.
func NewCHMReader(fallback interfaces.DocReader) *CHMReader {
	return &CHMReader{fallback: fallback}
}

// Read converts the archive and parses the resulting EPUB.
func (r *CHMReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if req == nil {
		return nil, errors.New("chm: empty request")
	}
	if len(req.FileContent) == 0 {
		return nil, errors.New("chm engine reads uploaded files; importing a .chm by URL is not supported")
	}
	if r.fallback == nil {
		return nil, errors.New("chm engine needs an EPUB parser: build with the anydoc tag or connect docreader")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	epub, err := chmToEPUB(req.FileContent, req.FileName)
	if err != nil {
		return nil, err
	}

	result, err := r.fallback.Read(ctx, &types.ReadRequest{
		FileContent:           epub,
		FileName:              chmEPUBFileName(req.FileName),
		FileType:              chmConvertedFileType,
		Title:                 req.Title,
		RequestID:             req.RequestID,
		ParserEngineOverrides: req.ParserEngineOverrides,
	})
	if err != nil {
		return nil, fmt.Errorf("parse the EPUB converted from %q: %w", req.FileName, err)
	}
	if result == nil {
		return nil, fmt.Errorf("parse the EPUB converted from %q: no result", req.FileName)
	}
	if strings.TrimSpace(result.MarkdownContent) == "" {
		return nil, fmt.Errorf("the EPUB converted from %q contains no extractable text", req.FileName)
	}

	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	if inner := result.Metadata["parser"]; inner != "" {
		result.Metadata["chm_epub_parser"] = inner
	}
	result.Metadata["parser"] = CHMEngineName
	result.Metadata["chm_converter"] = "chm2epub"
	result.Metadata["chm_epub_bytes"] = fmt.Sprintf("%d", len(epub))
	return result, nil
}

// chmToEPUB runs the converter over a temp copy of the archive. It works on a
// path because the CHM container is read with random access; the caller keeps
// the uploaded bytes in memory.
func chmToEPUB(data []byte, name string) ([]byte, error) {
	workDir, err := os.MkdirTemp("", "weknora-chm-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir for chm conversion: %w", err)
	}
	defer os.RemoveAll(workDir)

	src := filepath.Join(workDir, "source.chm")
	dst := filepath.Join(workDir, "converted.epub")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return nil, fmt.Errorf("write chm to temp dir: %w", err)
	}

	started := time.Now()
	out, err := chm2epub.One(src, chm2epub.Options{
		OutPath:   dst,
		Overwrite: true,
		// The converter's own logging is a CLI convenience; the pipeline logs
		// the outcome itself.
		Log: logx.New(io.Discard, logx.LevelError),
	})
	if err != nil {
		return nil, fmt.Errorf("chm2epub conversion failed for %q: %w", name, err)
	}

	epub, err := os.ReadFile(out)
	if err != nil {
		return nil, fmt.Errorf("read the EPUB written by chm2epub: %w", err)
	}
	if len(epub) == 0 {
		return nil, fmt.Errorf("chm2epub reported success but wrote an empty EPUB")
	}
	// Every EPUB is a ZIP; a different payload means we cannot parse it.
	if !bytes.HasPrefix(epub, []byte("PK")) {
		return nil, fmt.Errorf("chm2epub produced a file that is not an EPUB archive")
	}

	logger.Infof(context.Background(), "[chm] converted %q to EPUB (%d bytes) in %s",
		name, len(epub), time.Since(started).Round(time.Millisecond))
	return epub, nil
}

// chmEPUBFileName names the intermediate EPUB after the archive, so document
// titles and source references keep the original name instead of "source.chm".
func chmEPUBFileName(name string) string {
	ext := filepath.Ext(name)
	if ext == "" {
		return name + "." + chmConvertedFileType
	}
	return strings.TrimSuffix(name, ext) + "." + chmConvertedFileType
}

// preferLocalEngine is the registry's type-level default. CHM always belongs to
// the dedicated engine: no other engine reads the container, and letting it fall
// through would send an unsupported type to docreader.
func preferLocalEngine(fileType string) string {
	ft := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fileType), "."))
	if ft == "chm" {
		return CHMEngineName
	}
	return preferAnydocWhenAvailable(fileType)
}

// ---------------------------------------------------------------------------
// chm — in-process container conversion, anydoc/docreader for the EPUB
// ---------------------------------------------------------------------------

type chmEngine struct{}

func (e *chmEngine) Name() string { return CHMEngineName }

func (e *chmEngine) Description() string {
	return "CHM archive unpacked in-process to EPUB, then parsed by the in-process reader"
}

func (e *chmEngine) FileTypes(_ bool) []string { return []string{"chm"} }

func (e *chmEngine) CheckAvailable(_ bool, _ map[string]string) (bool, string) {
	return true, ""
}

func (e *chmEngine) NewReader(ctx context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	fallback := chmEPUBFallback(deps)
	if fallback == nil {
		return nil, errEngineUnavailable(CHMEngineName,
			"no EPUB parser available: build with the anydoc tag or connect docreader")
	}
	return NewCHMReader(fallback), nil
}

// chmEPUBFallback picks the engine that turns the intermediate EPUB into
// markdown: the in-process anydoc binding when this build links it, otherwise
// the docreader service.
func chmEPUBFallback(deps ReaderDeps) interfaces.DocReader {
	if anydoc.Available() && anydoc.Supports(chmConvertedFileType, "") {
		return NewAnydocReader(deps.Overrides, deps.Remote)
	}
	if deps.Remote != nil {
		return deps.Remote
	}
	return nil
}
