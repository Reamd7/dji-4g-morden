//go:build !hardware

package qmidatapath

import (
	"context"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/armon/go-socks5"
)

// TestSOCKS5ConnectHandshake verifies the SOCKS5 CONNECT flow with a mock
// dialer that routes to a local echo server. No USB or gVisor needed.
func TestSOCKS5ConnectHandshake(t *testing.T) {
	// 1. Start an echo server (simulates the "destination" on the 4G network)
	echoLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("echo listen: %v", err)
	}
	defer echoLn.Close()
	echoAddr := echoLn.Addr().String()
	go func() {
		c, err := echoLn.Accept()
		if err != nil {
			return
		}
		io.Copy(c, c) // echo
		c.Close()
	}()

	// 2. Mock dialer: routes to echo server (in real usage, goes to gVisor netstack)
	mockDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return net.Dial(network, echoAddr)
	}

	// 3. Start SOCKS5 server with mock dialer
	conf := &socks5.Config{Dial: mockDial}
	server, err := socks5.New(conf)
	if err != nil {
		t.Fatalf("socks5.New: %v", err)
	}
	socksLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("socks listen: %v", err)
	}
	defer socksLn.Close()
	go server.Serve(socksLn)

	// 4. Client: SOCKS5 handshake + CONNECT + echo round-trip
	client, err := net.Dial("tcp", socksLn.Addr().String())
	if err != nil {
		t.Fatalf("dial socks5: %v", err)
	}
	defer client.Close()
	client.SetDeadline(time.Now().Add(5 * time.Second))

	// SOCKS5 greeting: version 5, 1 auth method, no-auth (0x00)
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write greeting: %v", err)
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(client, resp); err != nil {
		t.Fatalf("read greeting resp: %v", err)
	}
	if resp[0] != 0x05 || resp[1] != 0x00 {
		t.Fatalf("greeting resp: %02x %02x, want 05 00", resp[0], resp[1])
	}

	// CONNECT request: ver=5, cmd=1(CONNECT), rsv=0, atyp=1(IPv4), ip+port
	// Target doesn't matter — mock dialer always routes to echo server
	req := []byte{0x05, 0x01, 0x00, 0x01, 127, 0, 0, 1, 0x1F, 0x90} // port 8080
	if _, err := client.Write(req); err != nil {
		t.Fatalf("write connect: %v", err)
	}
	rep := make([]byte, 10)
	if _, err := io.ReadFull(client, rep[:4]); err != nil {
		t.Fatalf("read connect resp header: %v", err)
	}
	if rep[1] != 0x00 {
		t.Fatalf("connect reply status=%d, want 0 (success)", rep[1])
	}
	// Drain the bind addr (4 more bytes for IPv4 + 2 port)
	atyp := rep[3]
	if atyp == 0x01 {
		io.ReadFull(client, rep[4:10])
	}

	// 5. Echo round-trip
	msg := []byte("hello socks5")
	if _, err := client.Write(msg); err != nil {
		t.Fatalf("write echo: %v", err)
	}
	buf := make([]byte, len(msg))
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(buf) != string(msg) {
		t.Fatalf("echo got %q, want %q", buf, msg)
	}
}

// TestRunSOCKS5ContextCancel verifies RunSOCKS5 exits cleanly on ctx cancel.
func TestRunSOCKS5ContextCancel(t *testing.T) {
	sink, err := NewNetstackPacketSink(netip.AddrFrom4([4]byte{10, 0, 0, 1}), 1500, false, netip.Addr{})
	if err != nil {
		t.Fatalf("NewNetstackPacketSink: %v", err)
	}
	defer sink.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- RunSOCKS5(ctx, sink, "127.0.0.1:0")
	}()

	// Cancel after a short delay
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("RunSOCKS5 returned %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("RunSOCKS5 did not exit after ctx cancel")
	}
}
