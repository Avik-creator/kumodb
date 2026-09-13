package main

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
func TestRunReturnsIfContextAlreadyCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := run(ctx, testLogger(), "127.0.0.1:0")
	if err != nil {
		t.Fatalf("run() error = %v, want nil", err)
	}
}

func TestRunReturnsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- run(ctx, testLogger(), "127.0.0.1:0")
	}()

	select {
	case <-done:
		t.Fatal("run returned before cancel")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("run did not return after cancel")
	}
}

func TestServeAcceptsConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, testLogger(), ln)
	}()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve() error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not return after cancel")
	}
}

func TestParseStartupHeader(t *testing.T) {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], 8)
	binary.BigEndian.PutUint32(b[4:8], 196608)

	length, version, err := parseStartupHeader(b)
	if err != nil {
		t.Fatal(err)
	}
	if length != 8 || version != 196608 {
		t.Fatalf("length=%d version=%d", length, version)
	}
}

func TestParseStartupHeaderTooShort(t *testing.T) {
	_, _, err := parseStartupHeader([]byte{1, 2, 3})
	if err == nil {
		t.Fatal("expected error")
	}
}
