package qmidatapath

import (
	"context"
	"fmt"
	"net"

	"github.com/armon/go-socks5"
)

// RunSOCKS5 starts a SOCKS5 server on listenAddr (host network, e.g. "127.0.0.1:1080").
// Outbound connections are dialed through the NetstackPacketSink's dialer
// (gVisor netstack → channel → USB → modem), NOT the host network.
//
// Blocks until ctx is canceled or the listener errors.
func RunSOCKS5(ctx context.Context, sink *NetstackPacketSink, listenAddr string) error {
	conf := &socks5.Config{
		Dial: sink.NetstackDialer(), // all CONNECT requests go through 4G
	}
	server, err := socks5.New(conf)
	if err != nil {
		return fmt.Errorf("socks5: New: %w", err)
	}

	// Custom listener so we can honor ctx cancellation.
	lc := net.ListenConfig{}
	ln, err := lc.Listen(ctx, "tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("socks5: listen %s: %w", listenAddr, err)
	}
	defer ln.Close()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		ln.Close() // unblock Serve
		<-errCh
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
