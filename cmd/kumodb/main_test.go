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

/*
	Same serve/Dial pattern as TestServeAcceptsConnection:

Write 8 bytes: length 8, sslRequestCode.
io.ReadFull(conn, buf[:1]) — must be 'N'.
Write 8 bytes: length 8, 196608.
cancel() and wait on serve.
*/
func TestParseSSLRequest(t *testing.T) {
	b := make([]byte, 8)
	binary.BigEndian.PutUint32(b[0:4], 8)
	binary.BigEndian.PutUint32(b[4:8], sslRequestCode)

	length, version, err := parseStartupHeader(b)
	if err != nil {
		t.Fatal(err)
	}

	if length != 8 || version != sslRequestCode {
		t.Fatalf("length=%d version=%d", length, version)
	}
}

func TestServeRefusesSSLThenReadsStartup(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, testLogger(), ln)
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	ssl := make([]byte, 8)
	binary.BigEndian.PutUint32(ssl[0:4], 8)
	binary.BigEndian.PutUint32(ssl[4:8], sslRequestCode)
	if _, err := conn.Write(ssl); err != nil {
		t.Fatalf("write ssl: %v", err)
	}

	var reply [1]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		t.Fatalf("read N: %v", err)
	}
	if reply[0] != 'N' {
		t.Fatalf("ssl reply = %q, want N", reply[0])
	}

	startup := make([]byte, 8)
	binary.BigEndian.PutUint32(startup[0:4], 8)
	binary.BigEndian.PutUint32(startup[4:8], 196608)
	if _, err := conn.Write(startup); err != nil {
		t.Fatalf("write startup: %v", err)
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

func TestRemainingStartupSize(t *testing.T) {
	n, err := remainingStartupSize(16)
	if err != nil {
		t.Fatal(err)
	}
	if n != 8 {
		t.Fatalf("n=%d, want 8", n)
	}

	n, err = remainingStartupSize(8)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("n=%d, want 0", n)
	}
}

func TestRemainingStartupSizeTooSmall(t *testing.T) {
	if _, err := remainingStartupSize(1); err == nil {
		t.Fatal("expected error")
	}
}

func TestRemainingStartupSizeTooLarge(t *testing.T) {
	if _, err := remainingStartupSize(100_000_000); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseStartupParams(t *testing.T) {
	body := []byte("user\x00avik\x00database\x00kumodb\x00\x00")
	got, err := parseStartupParams(body)
	if err != nil {
		t.Fatal(err)
	}
	if got["user"] != "avik" || got["database"] != "kumodb" {
		t.Fatalf("got %#v", got)
	}
}

func TestParseStartupParamsEmpty(t *testing.T) {
	got, err := parseStartupParams(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %#v", got)
	}
}

func TestParseStartupParamsMissingTerminator(t *testing.T) {
	body := []byte("user\x00avik\x00") // no final extra \0 after a complete pair...
	// actually user\0avik\0 is: key, value, then rest empty → missing terminator
	if _, err := parseStartupParams(body); err == nil {
		t.Fatal("expected error")
	}
}
