package docparser

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/infrastructure/docparser/anydoc"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	// CHMEngineName is the Calibre-backed CHM engine.
	CHMEngineName = "chm"
	// chmConvertPathOverride is the tenant-level setting naming the Calibre
	// ebook-convert executable.
	chmConvertPathOverride = "chm_ebook_convert_path"
	// chmConvertPathEnv is the deployment-wide default for the same path.
	chmConvertPathEnv = "CHM_EBOOK_CONVERT_PATH"
	// chmConvertTimeoutEnv bounds one conversion.
	chmConvertTimeoutEnv = "WEKNORA_CHM_CONVERT_TIMEOUT"
	// chmConvertedFileType is what the converter emits. EPUB is the only
	// Calibre output that both the anydoc binding and docreader parse.
	chmConvertedFileType = "epub"
	// chmMaxStderrTail caps the converter output carried into an error.
	chmMaxStderrTail = 800
)

// defaultCHMConvertTimeout is generous on purpose: a 5 MB manual with a
// thousand topics takes minutes because Calibre cold-starts its Python runtime
// and re-renders every topic, and it scales with the archive size.
const defaultCHMConvertTimeout = 15 * time.Minute

// CHMReader turns a compiled HTML Help archive into an EPUB with Calibre's
// ebook-convert, then hands the EPUB to whichever engine parses EPUBs in this
// deployment: the in-process anydoc binding, or the docreader service.
//
// CHM is a container (LZX-compressed HTML plus a .hhc topic index), not a
// document format, so no parser reads it directly. Calibre is the decompressor;
// the EPUB it writes keeps the topic order and embedded images.
type CHMReader struct {
	convertPath string
	timeout     time.Duration
	fallback    interfaces.DocReader
}

// NewCHMReader builds a reader. The converter path resolves the same way the
// availability check does, so a reader is only built once a path exists.
func NewCHMReader(overrides map[string]string, fallback interfaces.DocReader) *CHMReader {
	return &CHMReader{
		convertPath: ResolveCHMConvertPath(overrides),
		timeout:     requestTimeoutFromEnv(chmConvertTimeoutEnv, defaultCHMConvertTimeout),
		fallback:    fallback,
	}
}

// ResolveCHMConvertPath returns the configured ebook-convert executable: the
// tenant setting wins over the deployment environment variable.
func ResolveCHMConvertPath(overrides map[string]string) string {
	if path := strings.TrimSpace(overrides[chmConvertPathOverride]); path != "" {
		return path
	}
	return strings.TrimSpace(os.Getenv(chmConvertPathEnv))
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

	epub, err := r.toEPUB(ctx, req)
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
	result.Metadata["chm_converter"] = "calibre-ebook-convert"
	result.Metadata["chm_epub_bytes"] = strconv.Itoa(len(epub))
	return result, nil
}

// toEPUB runs ebook-convert in a private temp directory. Calibre is a Python
// program: isolating its config directory keeps a running Calibre GUI from
// locking the conversion out and leaves the user's library untouched.
func (r *CHMReader) toEPUB(ctx context.Context, req *types.ReadRequest) ([]byte, error) {
	exe, err := resolveCHMExecutable(r.convertPath)
	if err != nil {
		return nil, err
	}

	workDir, err := os.MkdirTemp("", "weknora-chm-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir for chm conversion: %w", err)
	}
	defer os.RemoveAll(workDir)

	src := filepath.Join(workDir, "source.chm")
	dst := filepath.Join(workDir, "converted.epub")
	if err := os.WriteFile(src, req.FileContent, 0o600); err != nil {
		return nil, fmt.Errorf("write chm to temp dir: %w", err)
	}

	runCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, exe, src, dst)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "CALIBRE_CONFIG_DIRECTORY="+filepath.Join(workDir, "calibre-config"))
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	// A killed converter can leave a child holding the output pipe; without a
	// wait delay Run would block past the deadline.
	cmd.WaitDelay = 5 * time.Second
	hideCHMConvertWindow(cmd)

	started := time.Now()
	runErr := cmd.Run()
	if runErr != nil {
		switch {
		case errors.Is(runCtx.Err(), context.DeadlineExceeded):
			return nil, fmt.Errorf("ebook-convert timed out after %s", r.timeout)
		case errors.Is(ctx.Err(), context.Canceled):
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ebook-convert failed: %w: %s",
			runErr, truncateTail(output.String(), chmMaxStderrTail))
	}

	epub, err := os.ReadFile(dst)
	if err != nil {
		return nil, fmt.Errorf("read the EPUB written by ebook-convert: %w", err)
	}
	if len(epub) == 0 {
		return nil, fmt.Errorf("ebook-convert reported success but wrote an empty EPUB: %s",
			truncateTail(output.String(), chmMaxStderrTail))
	}
	// Every EPUB is a ZIP; a different payload means the converter wrote
	// something we cannot parse (or a wrapper script printed to the output).
	if !bytes.HasPrefix(epub, []byte("PK")) {
		return nil, fmt.Errorf("ebook-convert produced a file that is not an EPUB archive")
	}

	logger.Infof(ctx, "[chm] converted %q to EPUB (%d bytes) in %s",
		req.FileName, len(epub), time.Since(started).Round(time.Millisecond))
	return epub, nil
}

// checkCHMConverter reports whether the engine can run for this tenant.
func checkCHMConverter(overrides map[string]string) (bool, string) {
	if ResolveCHMConvertPath(overrides) == "" {
		return false, "Calibre ebook-convert is not configured; set its path in Settings → Parser engines"
	}
	if _, err := resolveCHMExecutable(ResolveCHMConvertPath(overrides)); err != nil {
		return false, err.Error()
	}
	return true, ""
}

// resolveCHMExecutable validates the configured path. A bare name is looked up
// on PATH; anything with a separator must exist as a file. The converter is
// executed directly, never through a shell.
func resolveCHMExecutable(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("Calibre ebook-convert is not configured")
	}
	if strings.ContainsAny(path, `\/`) {
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("Calibre ebook-convert not found at %q: %w", path, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("Calibre ebook-convert path %q is a directory, not an executable", path)
		}
		return path, nil
	}
	exe, err := exec.LookPath(path)
	if err != nil {
		return "", fmt.Errorf("Calibre ebook-convert %q is not on PATH: %w", path, err)
	}
	return exe, nil
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

// truncateTail keeps the end of the converter output, where Calibre and the
// Python runtime write the actual failure.
func truncateTail(s string, limit int) string {
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return "..." + s[len(s)-limit:]
}

// preferLocalEngine is the registry's type-level default. CHM always belongs to
// the Calibre-backed engine: no other engine reads the container, and letting
// it fall through would send an unsupported type to docreader.
func preferLocalEngine(fileType string) string {
	ft := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(fileType), "."))
	if ft == "chm" {
		return CHMEngineName
	}
	return preferAnydocWhenAvailable(fileType)
}

// ---------------------------------------------------------------------------
// chm — Calibre ebook-convert for the container, anydoc/docreader for the EPUB
// ---------------------------------------------------------------------------

type chmEngine struct{}

func (e *chmEngine) Name() string { return CHMEngineName }

func (e *chmEngine) Description() string {
	return "CHM archive converted to EPUB with Calibre ebook-convert, then parsed in-process"
}

func (e *chmEngine) FileTypes(_ bool) []string { return []string{"chm"} }

func (e *chmEngine) CheckAvailable(_ bool, overrides map[string]string) (bool, string) {
	return checkCHMConverter(overrides)
}

func (e *chmEngine) NewReader(_ context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	if ok, reason := checkCHMConverter(deps.Overrides); !ok {
		return nil, errEngineUnavailable(CHMEngineName, reason)
	}
	return NewCHMReader(deps.Overrides, chmEPUBFallback(deps)), nil
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
