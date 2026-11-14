package docparser

import (
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// gRPC dials lazily, so a connection object exists even when nothing listens.
// IsConnected must therefore reflect the health service, otherwise the engine
// list advertises a usable docreader while every parse fails.
func TestGRPCDocumentReaderIsConnectedFollowsHealthStatus(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(server, healthServer)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	reader, err := NewGRPCDocumentReader(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if !reader.IsConnected() {
		t.Fatal("a serving docreader must be reported as connected")
	}

	// A cached probe would hide this transition; clear it to force a re-check.
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	reader.healthMu.Lock()
	reader.healthAt = time.Time{}
	reader.healthMu.Unlock()
	if reader.IsConnected() {
		t.Fatal("NOT_SERVING must be reported as disconnected")
	}
}

func TestGRPCDocumentReaderIsConnectedIsFalseWithoutService(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	// Close immediately: the address now refuses connections.
	_ = listener.Close()

	reader, err := NewGRPCDocumentReader(addr)
	if err != nil {
		t.Fatal(err)
	}
	if reader.IsConnected() {
		t.Fatal("a dead docreader must not be reported as connected")
	}
}

func TestGRPCDocumentReaderWithoutConnectionIsDisconnected(t *testing.T) {
	reader := &GRPCDocumentReader{}
	if reader.IsConnected() {
		t.Fatal("an unconfigured reader cannot be connected")
	}
}
