package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const sslRequestCode = 80877103 // postgres SSLRequest

func main() {

	addr := flag.String("addr", "127.0.0.1:15432", "the address to listen on")
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log, *addr); err != nil {
		log.Error("failed to run kumodb", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return serve(ctx, log, ln)
}

func serve(ctx context.Context, log *slog.Logger, ln net.Listener) error {
	defer ln.Close()

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	log.Info("starting kumodb", "addr", ln.Addr())

	var wg sync.WaitGroup
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return err
		}
		wg.Add(1)
		go func(c net.Conn) {
			defer wg.Done()
			handleConn(ctx, log, c)
		}(conn)
	}
	wg.Wait()
	return nil
}

func handleConn(ctx context.Context, log *slog.Logger, conn net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer conn.Close()

	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	log.Info("accepted", "remote", conn.RemoteAddr().String())

	length, version, err := readStartupHeader(conn)
	if err != nil {
		log.Error("failed to read startup header", "error", err)
		return
	}

	if version == sslRequestCode && length == 8 {
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			log.Error("failed to set write deadline", "error", err)
			return
		}

		if _, err := conn.Write([]byte{'N'}); err != nil {
			log.Error("failed to refuse SSL", "error", err)
			return
		}
		if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			log.Error("read deadline", "error", err)
			return
		}
		length, version, err = readStartupHeader(conn)
		if err != nil {
			log.Error("failed to read startup header", "error", err)
			return
		}
	}

	log.Info("startup header", "length", length, "version", version)

	log.Info("closed", "remote", conn.RemoteAddr().String())
}

func parseStartupHeader(b []byte) (length, version uint32, err error) {
	if len(b) < 8 {
		return 0, 0, fmt.Errorf("startup header too short: %d", len(b))
	}
	length = binary.BigEndian.Uint32(b[0:4])
	version = binary.BigEndian.Uint32(b[4:8])
	return length, version, nil
}

func readStartupHeader(conn net.Conn) (length, version uint32, err error) {
	const headerLen = 8
	header := make([]byte, headerLen)
	if _, err := io.ReadFull(conn, header); err != nil {
		return 0, 0, err
	}
	length, version, err = parseStartupHeader(header)
	if err != nil {
		return 0, 0, err
	}
	return length, version, nil
}
