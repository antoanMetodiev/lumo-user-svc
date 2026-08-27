package metrics

import (
	"context"
	"testing"
)

// TestStartDisabledIsNoOp covers the enabled=false branch — mirrors the Java
// service's export.enabled=false default. This must not touch the network
// or panic, and must hand back a harmless shutdown func.
func TestStartDisabledIsNoOp(t *testing.T) {
	shutdown, err := Start(context.Background(), false, "https://otlp.example.com/v1/metrics", "Basic xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected a non-nil shutdown func even when disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() = %v, want nil", err)
	}
}

// TestStartEnabledButNoEndpointIsNoOp covers enabled=true with a blank
// endpoint — must still disable cleanly (and log why) rather than trying to
// dial an empty URL.
func TestStartEnabledButNoEndpointIsNoOp(t *testing.T) {
	shutdown, err := Start(context.Background(), true, "", "Basic xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected a non-nil shutdown func when the endpoint is empty")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Errorf("shutdown() = %v, want nil", err)
	}
}
