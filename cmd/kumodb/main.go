package main

import (
	"context"
	"flag"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

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

	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	log.Info("accepted", "remote", conn.RemoteAddr().String())
	_, _ = io.Copy(conn, conn)
	log.Info("closed", "remote", conn.RemoteAddr().String())
}
