package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestRunReturnsIfContextAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-cancel context

	err := run(ctx, testLogger())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestRunReturnsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, testLogger())
	}()

	// 1. Verify that run() DOES NOT return immediately while context is active
	select {
	case <-done:
		t.Fatal("run() returned prematurely")
	case <-time.After(50 * time.Millisecond):
		// SUCCESS: run() is successfully blocking as expected
	}

	// 2. Cancel the context now that we know run() is blocking
	cancel()

	// 3. Verify that run() reacts to cancel() and exits cleanly
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("run did not return after cancel")
	}
}
