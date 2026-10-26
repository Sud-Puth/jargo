package wsutil

import (
	"bufio"
	"context"
	"crypto/sha1" //nolint:gosec // required by the WebSocket handshake (RFC 6455)
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// wsGUID is the key the WebSocket handshake hashes the client's key with.
const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// deafPeer accepts one WebSocket handshake and then never reads, so the
// client's write buffers fill and its sends wait on it.
func deafPeer(t *testing.T) string {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	t.Cleanup(func() {
		close(release)
		_ = ln.Close()
	})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		r := bufio.NewReader(conn)
		var key string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if name, value, ok := strings.Cut(line, ":"); ok && strings.EqualFold(name, "Sec-WebSocket-Key") {
				key = strings.TrimSpace(value)
			}
		}
		sum := sha1.Sum([]byte(key + wsGUID)) //nolint:gosec // required by the handshake
		_, _ = conn.Write([]byte("HTTP/1.1 101 Switching Protocols\r\n" +
			"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(sum[:]) + "\r\n\r\n"))
		<-release
	}()
	return "ws://" + ln.Addr().String()
}

// TestDialAppliesTheDefaultSendTimeout checks a dialed connection is bounded
// without the caller asking.
func TestDialAppliesTheDefaultSendTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = c.CloseNow()
	}))
	t.Cleanup(srv.Close)

	conn, err := Dial(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil, 0)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if conn.sendTimeout != DefaultSendTimeout {
		t.Errorf("send timeout = %s, want %s", conn.sendTimeout, DefaultSendTimeout)
	}
}

// TestASendToAPeerThatStopsReadingIsBounded checks, end to end, that a send
// which cannot drain gives up after the send timeout and drops the connection,
// rather than waiting until TCP notices.
func TestASendToAPeerThatStopsReadingIsBounded(t *testing.T) {
	conn, err := Dial(t.Context(), deafPeer(t), nil, 0)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	conn.SetSendTimeout(300 * time.Millisecond)

	chunk := make([]byte, 1<<20)
	started := time.Now()
	var sendErr error
	for range 1000 {
		if sendErr = conn.Write(context.Background(), websocket.MessageBinary, chunk); sendErr != nil {
			break
		}
	}
	elapsed := time.Since(started)

	if !errors.Is(sendErr, ErrSendTimeout) {
		t.Fatalf("send error = %v, want the send timeout", sendErr)
	}
	if elapsed > 5*time.Second {
		t.Errorf("the stalled send took %s, want it bounded near the timeout", elapsed)
	}
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("x")); err == nil {
		t.Error("the connection still takes sends after the stalled one dropped it")
	}
}

// TestSendTimeoutCanBeTurnedOff checks a send timeout of zero leaves sends
// unbounded.
func TestSendTimeoutCanBeTurnedOff(t *testing.T) {
	c := &Conn{sendTimeout: DefaultSendTimeout}
	c.SetSendTimeout(0)
	if c.sendTimeout != 0 {
		t.Errorf("send timeout = %s, want none", c.sendTimeout)
	}
}

// TestRefusedHandshakeKeepsTheResponseHeader checks a refusal carries the
// header of the response that refused it, where a provider may explain why.
func TestRefusedHandshakeKeepsTheResponseHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reason", "bad setting")
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	_, err := Dial(t.Context(), "ws"+strings.TrimPrefix(srv.URL, "http"), nil, 0)

	he, ok := errors.AsType[*HandshakeError](err)
	if !ok {
		t.Fatalf("error = %v, want a refused handshake", err)
	}
	if got := he.Header.Get("X-Reason"); got != "bad setting" {
		t.Errorf("X-Reason = %q, want the refusing response's header", got)
	}
}
