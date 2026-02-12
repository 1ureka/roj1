package tunnel

import (
	"context"
	"fmt"
	"net"

	"github.com/1ureka/1ureka.net.p2p/internal/transport"
	"github.com/1ureka/1ureka.net.p2p/internal/util"
)

// ListenAndServe starts the client-side virtual TCP service on localPort.
// For each accepted connection it computes a socketID and spawns a
// ClientSocketHandler goroutine. It blocks until ctx is cancelled.
func ListenAndServe(
	ctx context.Context,
	localPort int,
	tr *transport.Transport,
	dispatcher *Dispatcher,
) error {
	addr := fmt.Sprintf("127.0.0.1:%d", localPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	// Close the listener when context is done so Accept() returns an error.
	go func() {
		<-ctx.Done()
		listener.Close()
	}()

	util.Logf("虛擬服務已啟動，監聽 %s", addr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil // normal shutdown
			default:
				return fmt.Errorf("accept error: %w", err)
			}
		}

		socketID := util.SocketIDFromConn(conn)
		util.Logf("[%08x] 新連線 from %s", socketID, conn.RemoteAddr())

		go ClientSocketHandler(ctx, socketID, conn, tr, dispatcher)
	}
}
