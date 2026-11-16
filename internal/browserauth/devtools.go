package browserauth

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
)

// devToolsRelay exposes Chrome's loopback DevTools port over a private Unix
// socket (mode 0600) so a viewer running as the same user can attach for
// screencast and input without gmcli publishing a TCP port. Bytes are copied
// verbatim: the viewer speaks plain DevTools HTTP and WebSocket over the
// socket. Chrome checks the Host header, so viewers send Host: 127.0.0.1.
type devToolsRelay struct {
	path     string
	listener net.Listener
	target   string
	wg       sync.WaitGroup
	closed   chan struct{}
}

func newDevToolsRelay(path string, port int) (*devToolsRelay, error) {
	if path == "" {
		return nil, errors.New("empty DevTools socket path")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve DevTools socket path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create DevTools socket directory: %w", err)
	}
	// A stale socket from an interrupted run would make Listen fail; only a
	// socket file is removed, never a regular file.
	if info, statErr := os.Lstat(path); statErr == nil {
		if info.Mode().Type() != os.ModeSocket {
			return nil, fmt.Errorf("DevTools socket path %s exists and is not a socket", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("remove stale DevTools socket: %w", err)
		}
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on DevTools socket: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("restrict DevTools socket: %w", err)
	}
	r := &devToolsRelay{path: path, listener: listener, target: fmt.Sprintf("127.0.0.1:%d", port), closed: make(chan struct{})}
	r.wg.Add(1)
	go r.accept()
	return r, nil
}

func (r *devToolsRelay) accept() {
	defer r.wg.Done()
	for {
		conn, err := r.listener.Accept()
		if err != nil {
			return
		}
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			r.relay(conn)
		}()
	}
}

func (r *devToolsRelay) relay(client net.Conn) {
	defer client.Close()
	upstream, err := net.Dial("tcp", r.target)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{}, 2)
	copyHalf := func(dst, src net.Conn) {
		_, _ = io.Copy(dst, src)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go copyHalf(upstream, client)
	go copyHalf(client, upstream)
	select {
	case <-done:
	case <-r.closed:
	}
	// Closing both ends unblocks the remaining copier.
	client.Close()
	upstream.Close()
	<-done
}

// Close stops accepting, drops open connections, and removes the socket file.
func (r *devToolsRelay) Close() error {
	select {
	case <-r.closed:
		return nil
	default:
		close(r.closed)
	}
	err := r.listener.Close()
	r.wg.Wait()
	if rmErr := os.Remove(r.path); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) && err == nil {
		err = rmErr
	}
	return err
}

// waitClosed blocks until Close completes or ctx ends; used by tests.
func (r *devToolsRelay) waitClosed(ctx context.Context) error {
	select {
	case <-r.closed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
