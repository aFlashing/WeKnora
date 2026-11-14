package docparser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// DoclingEngineName is the Docling engine, served by a local docling-serve.
	DoclingEngineName = "docling"
	// defaultDoclingTimeout bounds one conversion. CPU-only Docling needs
	// roughly 1-3s per page, so a few hundred pages can run for a while.
	defaultDoclingTimeout = 30 * time.Minute
	// doclingHealthTimeout keeps the engine list responsive when the service is
	// not running: an unreachable port only costs this much per refresh.
	doclingHealthTimeout = 3 * time.Second
)

// DoclingReader converts documents through a docling-serve instance.
//
// Docling (IBM, MIT) is a layout-aware parser: it returns reading order, table
// structure, formulas and page provenance instead of a flat text dump, which is
// what the in-process engines cannot do for PDFs. It runs as a separate Python
// service because the models need PyTorch.
//
// Images come back inline as data URIs (image_export_mode=embedded); the
// ingestion pipeline's ImageResolver stores them and rewrites the markdown, so
// this reader never has to know about object storage.
type DoclingReader struct {
	endpoint  string
	apiKey    string
	ocr       bool
	tableMode string
	client    *http.Client
}

// DoclingEndpoint resolves the service address: the tenant setting wins, and a
// DOCLING_ENDPOINT environment variable is the deployment default. The env
// fallback keeps desktop installs working without any per-tenant configuration
// and lets other local projects share the same service.
func DoclingEndpoint(overrides map[string]string) string {
	if endpoint := strings.TrimRight(strings.TrimSpace(overrides["docling_endpoint"]), "/"); endpoint != "" {
		return endpoint
	}
	return strings.TrimRight(strings.TrimSpace(os.Getenv("DOCLING_ENDPOINT")), "/")
}

// NewDoclingReader builds a reader from ParserEngineOverrides.
func NewDoclingReader(overrides map[string]string) *DoclingReader {
	return &DoclingReader{
		endpoint:  DoclingEndpoint(overrides),
		apiKey:    strings.TrimSpace(overrides["docling_api_key"]),
		ocr:       parseBoolOr(overrides["docling_ocr"], true),
		tableMode: stringOr(strings.ToLower(strings.TrimSpace(overrides["docling_table_mode"])), "accurate"),
		client: utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
			Timeout:            requestTimeoutFromEnv("WEKNORA_DOCLING_TIMEOUT", defaultDoclingTimeout),
			MaxRedirects:       2,
			DisableCompression: true, // a gzip'd 100MB markdown would just cost memory
		}),
	}
}

// Read uploads the document to docling-serve and returns its markdown.
func (r *DoclingReader) Read(ctx context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	if req == nil {
		return nil, errors.New("docling: empty request")
	}
	if r.endpoint == "" {
		return nil, errors.New("docling: service endpoint is not configured (set it in Settings → Parser engines)")
	}
	if len(req.FileContent) == 0 {
		return nil, errors.New("docling engine reads uploaded files; importing by URL is not supported")
	}

	resp, err := r.convert(ctx, req)
	if err != nil {
		return nil, err
	}
	if resp.Document.MDContent == nil || strings.TrimSpace(*resp.Document.MDContent) == "" {
		return nil, fmt.Errorf("docling returned no markdown for %q", req.FileName)
	}

	metadata := map[string]string{
		"parser":         DoclingEngineName,
		"docling_status": resp.Status,
	}
	if resp.ProcessingTime > 0 {
		metadata["docling_processing_time"] = strconv.FormatFloat(resp.ProcessingTime, 'f', 2, 64)
	}
	if resp.Confidence != nil && resp.Confidence.MeanScore != nil {
		metadata["docling_confidence"] = strconv.FormatFloat(*resp.Confidence.MeanScore, 'f', 3, 64)
	}

	return &types.ReadResult{
		MarkdownContent: *resp.Document.MDContent,
		Metadata:        metadata,
	}, nil
}

// doclingConvertResponse mirrors the fields of ConvertDocumentResponse this
// engine reads. Everything else (timings, per-page scores) is ignored.
type doclingConvertResponse struct {
	Document struct {
		Filename  string  `json:"filename"`
		MDContent *string `json:"md_content"`
	} `json:"document"`
	Status         string            `json:"status"`
	Errors         []json.RawMessage `json:"errors"`
	ProcessingTime float64           `json:"processing_time"`
	Confidence     *struct {
		MeanScore *float64 `json:"mean_score"`
	} `json:"confidence"`
}

func (r *DoclingReader) convert(ctx context.Context, req *types.ReadRequest) (*doclingConvertResponse, error) {
	// Submit asynchronously first. The sync endpoint gives up after
	// DOCLING_SERVE_MAX_SYNC_WAIT (120s by default) and answers 504 while the
	// conversion keeps running, so a CPU-only server re-queues the same work on
	// every retry and never catches up.
	started := time.Now()
	taskID, err := r.submitAsync(ctx, req)
	if errors.Is(err, errDoclingAsyncUnsupported) {
		logger.Infof(ctx, "[docling] async endpoint unavailable, falling back to the sync call")
		return r.convertSync(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	if err := r.waitForTask(ctx, taskID); err != nil {
		return nil, err
	}
	out, err := r.fetchResult(ctx, taskID)
	if err != nil {
		return nil, err
	}
	logger.Infof(ctx, "[docling] converted %q in %s via task %s (status=%s)",
		req.FileName, time.Since(started).Round(time.Millisecond), taskID, out.Status)
	return out, nil
}

// errDoclingAsyncUnsupported marks servers too old to expose /async.
var errDoclingAsyncUnsupported = errors.New("docling: async conversion endpoint not available")

// convertSync is the pre-async path, kept for older docling-serve builds.
func (r *DoclingReader) convertSync(ctx context.Context, req *types.ReadRequest) (*doclingConvertResponse, error) {
	started := time.Now()
	body, status, err := r.postFile(ctx, "/v1/convert/file", req)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("docling returned HTTP %d: %s", status, truncateTail(string(body), 300))
	}
	var out doclingConvertResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("docling returned an unreadable response: %w", err)
	}
	logger.Infof(ctx, "[docling] converted %q in %s (status=%s, %d bytes markdown)",
		req.FileName, time.Since(started).Round(time.Millisecond), out.Status, len(body))
	if out.Status != "success" {
		return nil, fmt.Errorf("docling reported %q: %s", out.Status, doclingErrorText(out.Errors))
	}
	return &out, nil
}

// postFile uploads the document and returns the response body. The body is
// streamed from the caller's bytes so a large PDF is not copied a second time.
func (r *DoclingReader) postFile(
	ctx context.Context, path string, req *types.ReadRequest,
) ([]byte, int, error) {
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	writeErr := make(chan error, 1)
	go func() {
		err := r.writeForm(writer, req)
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		// The request body ends only when the pipe closes: without this the
		// server waits for more parts and the call hangs until the timeout.
		if err != nil {
			_ = pw.CloseWithError(err)
		} else {
			_ = pw.Close()
		}
		writeErr <- err
	}()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint+path, pr)
	if err != nil {
		pr.CloseWithError(err)
		return nil, 0, fmt.Errorf("docling: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())
	httpReq.Header.Set("Accept", "application/json")
	if r.apiKey != "" {
		httpReq.Header.Set("X-Api-Key", r.apiKey)
	}

	resp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("docling request failed: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err := <-writeErr; err != nil {
		return nil, resp.StatusCode, fmt.Errorf("docling: build form: %w", err)
	}
	if readErr != nil {
		return nil, resp.StatusCode, fmt.Errorf("docling: read response: %w", readErr)
	}
	return body, resp.StatusCode, nil
}

// submitAsync queues a conversion and returns its task id.
func (r *DoclingReader) submitAsync(ctx context.Context, req *types.ReadRequest) (string, error) {
	body, status, err := r.postFile(ctx, "/v1/convert/file/async", req)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound || status == http.StatusMethodNotAllowed {
		return "", errDoclingAsyncUnsupported
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("docling returned HTTP %d: %s", status, truncateTail(string(body), 300))
	}
	var accepted struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		Position   int    `json:"task_position"`
	}
	if err := json.Unmarshal(body, &accepted); err != nil {
		return "", fmt.Errorf("docling returned an unreadable task: %w", err)
	}
	if strings.TrimSpace(accepted.TaskID) == "" {
		return "", fmt.Errorf("docling accepted the upload but returned no task id")
	}
	logger.Infof(ctx, "[docling] queued %q as task %s (%s, position %d)",
		req.FileName, accepted.TaskID, accepted.TaskStatus, accepted.Position)
	return accepted.TaskID, nil
}

// waitForTask polls until the task reaches a terminal state or ctx expires.
func (r *DoclingReader) waitForTask(ctx context.Context, taskID string) error {
	interval := 2 * time.Second
	const maxInterval = 10 * time.Second
	lastPosition := -1
	for {
		status, position, message, err := r.taskStatus(ctx, taskID)
		if err != nil {
			return err
		}
		switch status {
		case "success":
			return nil
		case "failure", "revoked":
			if message == "" {
				message = "no detail returned"
			}
			return fmt.Errorf("docling task %s ended as %s: %s", taskID, status, message)
		}
		if position > 0 && position != lastPosition {
			logger.Infof(ctx, "[docling] task %s %s, position %d", taskID, status, position)
			lastPosition = position
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("docling task %s still %s: %w", taskID, status, ctx.Err())
		case <-time.After(interval):
		}
		if interval < maxInterval {
			interval += time.Second
		}
	}
}

func (r *DoclingReader) taskStatus(ctx context.Context, taskID string) (string, int, string, error) {
	body, status, err := r.get(ctx, r.endpoint+"/v1/status/poll/"+taskID)
	if err != nil {
		return "", 0, "", err
	}
	if status != http.StatusOK {
		return "", 0, "", fmt.Errorf("docling status check returned HTTP %d: %s", status, truncateTail(string(body), 200))
	}
	var out struct {
		TaskStatus   string `json:"task_status"`
		TaskPosition int    `json:"task_position"`
		ErrorMessage string `json:"error_message"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", 0, "", fmt.Errorf("docling returned an unreadable task status: %w", err)
	}
	return out.TaskStatus, out.TaskPosition, out.ErrorMessage, nil
}

func (r *DoclingReader) fetchResult(ctx context.Context, taskID string) (*doclingConvertResponse, error) {
	body, status, err := r.get(ctx, r.endpoint+"/v1/result/"+taskID)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("docling result returned HTTP %d: %s", status, truncateTail(string(body), 300))
	}
	var out doclingConvertResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("docling returned an unreadable result: %w", err)
	}
	if out.Status != "success" {
		return nil, fmt.Errorf("docling reported %q: %s", out.Status, doclingErrorText(out.Errors))
	}
	return &out, nil
}

// get performs an authenticated GET and returns the body with its status.
func (r *DoclingReader) get(ctx context.Context, url string) ([]byte, int, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("docling: build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	if r.apiKey != "" {
		httpReq.Header.Set("X-Api-Key", r.apiKey)
	}
	resp, err := r.client.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("docling request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("docling: read response: %w", err)
	}
	return body, resp.StatusCode, nil
}

// writeForm fills the multipart form. Exact option names come from the
// docling-serve OpenAPI schema (/v1/convert/file).
func (r *DoclingReader) writeForm(writer *multipart.Writer, req *types.ReadRequest) error {
	name := req.FileName
	if name == "" {
		name = "document." + strings.TrimPrefix(strings.TrimSpace(req.FileType), ".")
	}
	part, err := writer.CreateFormFile("files", name)
	if err != nil {
		return err
	}
	if _, err := part.Write(req.FileContent); err != nil {
		return err
	}

	fields := map[string]string{
		"to_formats": "md",
		// Inline images let the pipeline's ImageResolver store them; see the
		// DoclingReader doc comment.
		"image_export_mode":  "embedded",
		"do_ocr":             strconv.FormatBool(r.ocr),
		"do_table_structure": "true",
		"table_mode":         r.tableMode,
		"abort_on_error":     "false",
	}
	for key, value := range fields {
		if value == "" {
			continue
		}
		if err := writer.WriteField(key, value); err != nil {
			return err
		}
	}
	return nil
}

// doclingErrorText flattens the errors array into one line for the UI.
func doclingErrorText(raw []json.RawMessage) string {
	if len(raw) == 0 {
		return "no error detail returned"
	}
	parts := make([]string, 0, len(raw))
	for _, item := range raw {
		var entry struct {
			ErrorMessage string `json:"error_message"`
			Message      string `json:"message"`
		}
		if err := json.Unmarshal(item, &entry); err == nil {
			switch {
			case entry.ErrorMessage != "":
				parts = append(parts, entry.ErrorMessage)
				continue
			case entry.Message != "":
				parts = append(parts, entry.Message)
				continue
			}
		}
		parts = append(parts, string(item))
	}
	return truncateTail(strings.Join(parts, "; "), 300)
}

// truncateTail keeps the last n runes of s: the informative part of a server
// error usually sits at the end of the body.
func truncateTail(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return "…" + string(r[len(r)-n:])
}

// PingDocling reports whether a docling-serve answers on this endpoint.
func PingDocling(endpoint, apiKey string) (bool, string) {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		return false, "Docling service not configured"
	}
	client := utils.NewSSRFSafeHTTPClient(utils.SSRFSafeHTTPClientConfig{
		Timeout:      doclingHealthTimeout,
		MaxRedirects: 1,
	})
	req, err := http.NewRequest(http.MethodGet, endpoint+"/health", nil)
	if err != nil {
		return false, "Invalid Docling endpoint: " + err.Error()
	}
	if strings.TrimSpace(apiKey) != "" {
		req.Header.Set("X-Api-Key", strings.TrimSpace(apiKey))
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, "Cannot reach Docling service: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("Docling health check returned HTTP %d", resp.StatusCode)
	}
	return true, ""
}

// ---------------------------------------------------------------------------
// docling — layout-aware parsing through a local docling-serve
// ---------------------------------------------------------------------------

type doclingEngine struct{}

func (e *doclingEngine) Name() string { return DoclingEngineName }

func (e *doclingEngine) Description() string {
	return "Docling layout-aware parser via docling-serve (tables, formulas, reading order)"
}

// FileTypes stays on the formats Docling is uniquely good at. Office documents
// are parsed by anydoc in-process, which is far faster and already accurate;
// advertising them here would only invite slower parses.
func (e *doclingEngine) FileTypes(_ bool) []string {
	return []string{"pdf", "jpg", "jpeg", "png", "bmp", "tiff", "webp"}
}

func (e *doclingEngine) CheckAvailable(_ bool, overrides map[string]string) (bool, string) {
	endpoint := DoclingEndpoint(overrides)
	if endpoint == "" {
		return false, "Docling service not configured. Start docling-serve and set its address in Settings → Parser engines."
	}
	return PingDocling(endpoint, overrides["docling_api_key"])
}

func (e *doclingEngine) NewReader(ctx context.Context, deps ReaderDeps) (interfaces.DocReader, error) {
	if DoclingEndpoint(deps.Overrides) == "" {
		return nil, errEngineUnavailable(DoclingEngineName, "endpoint not configured")
	}
	return NewDoclingReader(deps.Overrides), nil
}
