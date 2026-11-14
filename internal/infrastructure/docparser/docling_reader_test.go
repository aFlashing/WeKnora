package docparser

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
)

// allowLoopbackForTest lets the SSRF-safe client reach the httptest listener.
func allowLoopbackForTest(t *testing.T) {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "127.0.0.1,::1")
	t.Setenv("SSRF_WHITELIST_EXTRA", "")
	utils.ResetSSRFWhitelistForTest()
	t.Cleanup(utils.ResetSSRFWhitelistForTest)
}

func doclingTestServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	allowLoopbackForTest(t)
	server := httptest.NewServer(http.HandlerFunc(handler))
	t.Cleanup(server.Close)
	return server
}

func TestDoclingReaderSendsLayoutOptionsAndReturnsMarkdown(t *testing.T) {
	var gotFields map[string]string
	var gotFilename, gotContent string
	server := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		// No async endpoint: the reader must fall back to the sync call.
		if strings.HasSuffix(r.URL.Path, "/async") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path != "/v1/convert/file" {
			t.Errorf("path = %q, want /v1/convert/file", r.URL.Path)
		}
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("parse form: %v", err)
			return
		}
		file, header, err := r.FormFile("files")
		if err != nil {
			t.Errorf("form file: %v", err)
			return
		}
		defer file.Close()
		content, _ := io.ReadAll(file)
		gotFilename, gotContent = header.Filename, string(content)
		gotFields = map[string]string{
			"to_formats":         r.FormValue("to_formats"),
			"image_export_mode":  r.FormValue("image_export_mode"),
			"do_ocr":             r.FormValue("do_ocr"),
			"do_table_structure": r.FormValue("do_table_structure"),
			"table_mode":         r.FormValue("table_mode"),
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"document":        map[string]any{"filename": "manual.pdf", "md_content": "# Title\n\n![](data:image/png;base64,AAA)"},
			"status":          "success",
			"processing_time": 12.5,
			"confidence":      map[string]any{"mean_score": 0.93},
		})
	})

	reader := NewDoclingReader(map[string]string{"docling_endpoint": server.URL})
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF-1.7 fake"), FileName: "manual.pdf", FileType: "pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.MarkdownContent, "# Title") {
		t.Fatalf("markdown = %q", result.MarkdownContent)
	}
	if gotFilename != "manual.pdf" || gotContent != "%PDF-1.7 fake" {
		t.Fatalf("uploaded file = %q %q", gotFilename, gotContent)
	}
	for key, want := range map[string]string{
		"to_formats":         "md",
		"image_export_mode":  "embedded",
		"do_ocr":             "true",
		"do_table_structure": "true",
		"table_mode":         "accurate",
	} {
		if got := gotFields[key]; got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if result.Metadata["parser"] != DoclingEngineName {
		t.Fatalf("parser = %q", result.Metadata["parser"])
	}
	if result.Metadata["docling_processing_time"] != "12.50" {
		t.Fatalf("processing time = %q", result.Metadata["docling_processing_time"])
	}
	if result.Metadata["docling_confidence"] != "0.930" {
		t.Fatalf("confidence = %q", result.Metadata["docling_confidence"])
	}
}

// The sync endpoint gives up after DOCLING_SERVE_MAX_SYNC_WAIT (120s) while the
// conversion keeps running server-side, so the reader submits a task and polls.
func TestDoclingReaderUsesAsyncTaskFlow(t *testing.T) {
	var submitted, polls int
	server := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/convert/file/async"):
			submitted++
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id": "task-1", "task_status": "pending", "task_position": 2,
			})
		case strings.HasPrefix(r.URL.Path, "/v1/status/poll/task-1"):
			polls++
			status, position := "pending", 1
			if polls >= 2 {
				status, position = "success", 0
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id": "task-1", "task_status": status, "task_position": position,
			})
		case strings.HasPrefix(r.URL.Path, "/v1/result/task-1"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"document": map[string]any{"md_content": "# Async\n\nbody"},
				"status":   "success",
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	reader := NewDoclingReader(map[string]string{"docling_endpoint": server.URL})
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF"), FileName: "a.pdf", FileType: "pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MarkdownContent != "# Async\n\nbody" {
		t.Fatalf("markdown = %q", result.MarkdownContent)
	}
	if submitted != 1 {
		t.Fatalf("async submissions = %d, want 1", submitted)
	}
	if polls < 2 {
		t.Fatalf("status polls = %d, want at least 2", polls)
	}
}

func TestDoclingReaderReportsFailedAsyncTask(t *testing.T) {
	server := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/convert/file/async"):
			_ = json.NewEncoder(w).Encode(map[string]any{"task_id": "task-2", "task_status": "pending"})
		case strings.HasPrefix(r.URL.Path, "/v1/status/poll/task-2"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"task_id": "task-2", "task_status": "failure",
				"error_message": "the document is encrypted",
			})
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	})

	reader := NewDoclingReader(map[string]string{"docling_endpoint": server.URL})
	_, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF"), FileName: "a.pdf", FileType: "pdf",
	})
	if err == nil || !strings.Contains(err.Error(), "the document is encrypted") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoclingReaderHonoursOverrides(t *testing.T) {
	var ocr, tableMode string
	server := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/async") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			t.Errorf("parse form: %v", err)
			return
		}
		ocr, tableMode = r.FormValue("do_ocr"), r.FormValue("table_mode")
		_, _ = w.Write([]byte(`{"document":{"md_content":"ok"},"status":"success"}`))
	})

	reader := NewDoclingReader(map[string]string{
		"docling_endpoint":   server.URL,
		"docling_ocr":        "false",
		"docling_table_mode": "fast",
	})
	if _, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF"), FileName: "a.pdf", FileType: "pdf",
	}); err != nil {
		t.Fatal(err)
	}
	if ocr != "false" || tableMode != "fast" {
		t.Fatalf("ocr=%q table_mode=%q", ocr, tableMode)
	}
}

func TestDoclingReaderSurfacesServiceErrors(t *testing.T) {
	server := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/async") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"document": map[string]any{"md_content": nil},
			"status":   "failure",
			"errors":   []map[string]any{{"error_message": "page 3 exceeded the time budget"}},
		})
	})
	reader := NewDoclingReader(map[string]string{"docling_endpoint": server.URL})
	_, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF"), FileName: "a.pdf", FileType: "pdf",
	})
	if err == nil || !strings.Contains(err.Error(), "page 3 exceeded") {
		t.Fatalf("err = %v", err)
	}
}

func TestDoclingReaderRejectsMissingInput(t *testing.T) {
	reader := NewDoclingReader(nil)
	if _, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: []byte("%PDF"), FileName: "a.pdf",
	}); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("err = %v, want a missing-endpoint error", err)
	}
	reader = NewDoclingReader(map[string]string{"docling_endpoint": "http://docling.local:5001"})
	if _, err := reader.Read(context.Background(), &types.ReadRequest{
		URL: "https://example.com/a.pdf", FileName: "a.pdf",
	}); err == nil {
		t.Fatal("expected URL imports to be rejected")
	}
}

func TestPingDocling(t *testing.T) {
	ok := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	if available, reason := PingDocling(ok.URL, ""); !available {
		t.Fatalf("healthy service reported unavailable: %s", reason)
	}

	bad := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	if available, reason := PingDocling(bad.URL, ""); available || reason == "" {
		t.Fatalf("available=%v reason=%q", available, reason)
	}

	if available, _ := PingDocling("", ""); available {
		t.Fatal("empty endpoint must be unavailable")
	}
}

// TestDoclingConversionEndToEnd talks to a real docling-serve; it is skipped
// unless both the endpoint and a sample PDF are supplied.
func TestDoclingConversionEndToEnd(t *testing.T) {
	endpoint := os.Getenv("WEKNORA_TEST_DOCLING_ENDPOINT")
	file := os.Getenv("WEKNORA_TEST_DOCLING_FILE")
	if endpoint == "" || file == "" {
		t.Skip("set WEKNORA_TEST_DOCLING_ENDPOINT and WEKNORA_TEST_DOCLING_FILE to run")
	}
	allowLoopbackForTest(t)
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	reader := NewDoclingReader(map[string]string{"docling_endpoint": endpoint})
	result, err := reader.Read(context.Background(), &types.ReadRequest{
		FileContent: data, FileName: filepath.Base(file), FileType: "pdf",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(result.MarkdownContent) == "" {
		t.Fatal("empty markdown")
	}
	if result.Metadata["parser"] != DoclingEngineName {
		t.Fatalf("parser = %q", result.Metadata["parser"])
	}
	t.Logf("converted %s to %d bytes of markdown (confidence=%s)",
		filepath.Base(file), len(result.MarkdownContent), result.Metadata["docling_confidence"])
}

func TestDoclingEngineAdvertisesPdfAndHealthGatesAvailability(t *testing.T) {
	server := doclingTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	var found *types.ParserEngineInfo
	for _, engine := range ListAllEngines(false, map[string]string{"docling_endpoint": server.URL}, nil) {
		if engine.Name == DoclingEngineName {
			e := engine
			found = &e
		}
	}
	if found == nil {
		t.Fatal("docling engine missing from the engine list")
	}
	if !found.Available {
		t.Fatalf("docling engine unavailable: %s", found.UnavailableReason)
	}
	hasPDF := false
	for _, ft := range found.FileTypes {
		if ft == "pdf" {
			hasPDF = true
		}
	}
	if !hasPDF {
		t.Fatalf("file types = %v, want pdf", found.FileTypes)
	}

	for _, engine := range ListAllEngines(false, nil, nil) {
		if engine.Name == DoclingEngineName && engine.Available {
			t.Fatal("docling must be unavailable without an endpoint")
		}
	}
}
