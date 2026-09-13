package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
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

const maxStartupLen = 10_000

// parseStartupParams parses the startup parameters from the startup body. It returns a map of key-value pairs. The body is the startup body as read from the client. The body is terminated by a 0 byte.
func parseStartupParams(body []byte) (map[string]string, error) {
	if len(body) == 0 {
		return make(map[string]string), nil
	}
	out := make(map[string]string)
	rest := body
	for {
		if len(rest) == 0 {
			return nil, fmt.Errorf("startup params: missing terminator")
		}
		if rest[0] == 0 {
			if len(rest) != 1 {
				return nil, fmt.Errorf("startup params: trailing garbage")
			}
			return out, nil
		}
		kEnd := bytes.IndexByte(rest, 0)
		if kEnd < 0 {
			return nil, fmt.Errorf("startup params: unterminated key")
		}
		key := string(rest[:kEnd])
		rest = rest[kEnd+1:]

		vEnd := bytes.IndexByte(rest, 0)
		if vEnd < 0 {
			return nil, fmt.Errorf("startup params: unterminated value")
		}
		val := string(rest[:vEnd])
		rest = rest[vEnd+1:]

		out[key] = val
	}
}

func writeMessage(w io.Writer, typ byte, payload []byte) error {
	length := uint32(4 + len(payload))
	buf := make([]byte, 1+int(length))
	buf[0] = typ
	binary.BigEndian.PutUint32(buf[1:5], length)
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

func remainingStartupSize(length uint32) (int, error) {
	if length < 8 {
		return 0, fmt.Errorf("startup length %d is too small", length)
	}
	if length > maxStartupLen {
		return 0, fmt.Errorf("startup length %d is too large", length)
	}
	return int(length) - 8, nil
}

func remainingMessageSize(length uint32) (int, error) {
	if length < 4 {
		return 0, fmt.Errorf("message length %d is too small", length)
	}

	if length > maxStartupLen {
		return 0, fmt.Errorf("message length %d is too large", length)
	}
	return int(length) - 4, nil
}

func readMessage(r io.Reader) (typ byte, payload []byte, err error) {
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	typ = header[0]
	n, err := remainingMessageSize(binary.BigEndian.Uint32(header[1:5]))
	if err != nil {
		return 0, nil, err
	}
	payload = make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return typ, payload, nil
}

func queryString(payload []byte) (string, error) {
	if len(payload) == 0 || payload[len(payload)-1] != 0 {
		return "", fmt.Errorf("query string: missing terminator")
	}
	return string(payload[:len(payload)-1]), nil
}

func errorResponsePayload(sqlstate, msg string) []byte {
	buf := bytes.NewBuffer(make([]byte, 0, len(sqlstate)+len(msg)+3))
	buf.WriteByte('S')
	buf.WriteString("ERROR")
	buf.WriteByte(0)
	buf.WriteByte('C')
	buf.WriteString(sqlstate)
	buf.WriteByte(0)
	buf.WriteByte('M')
	buf.WriteString(msg)
	buf.WriteByte(0)
	buf.WriteByte(0)
	return buf.Bytes()
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
	defer log.Info("closed", "remote", conn.RemoteAddr().String())

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

	remaining, err := remainingStartupSize(length)
	if err != nil {
		log.Error("failed to get remaining startup size", "error", err)
		return
	}
	body := make([]byte, remaining)
	if remaining > 0 {
		if _, err := io.ReadFull(conn, body); err != nil {
			log.Error("failed to read startup bytes", "error", err)
			return
		}
		log.Info("startup bytes", "bytes", remaining)

		params, err := parseStartupParams(body)
		if err != nil {
			log.Error("failed to parse startup params", "error", err)
			return
		}
		log.Info("startup params", "user", params["user"], "database", params["database"])
	}

	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Error("failed to set write deadline", "error", err)
		return
	}
	auth := make([]byte, 4)
	binary.BigEndian.PutUint32(auth, 0)
	if err := writeMessage(conn, 'R', auth); err != nil {
		log.Error("failed to write auth message", "error", err)
		return
	}
	if err := writeMessage(conn, 'Z', []byte{'I'}); err != nil {
		log.Error("failed to write ready for query", "error", err)
		return
	}

	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		log.Error("failed to clear read deadline", "error", err)
		return
	}

	for {
		typ, payload, err := readMessage(conn)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
				return
			}
			log.Error("failed to read message", "error", err)
			return
		}
		switch typ {
		case 'X':
			return
		case 'Q':
			sql, err := queryString(payload)
			if err != nil {
				log.Error("failed to parse query", "error", err)
				return
			}
			log.Info("query", "sql", sql)
			if err := refuseQuery(conn); err != nil {
				log.Error("failed to refuse query", "error", err)
				return
			}
		default:
			if err := refuseQuery(conn); err != nil {
				log.Error("failed to refuse query", "error", err)
				return
			}
		}
	}
}

func refuseQuery(conn net.Conn) error {
	if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return err
	}
	if err := writeMessage(conn, 'E', errorResponsePayload("0A000", "query forwarding not implemented")); err != nil {
		return err
	}
	return writeMessage(conn, 'Z', []byte{'I'})
}
