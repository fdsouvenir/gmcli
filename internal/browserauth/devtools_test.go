package browserauth

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// shortSocketPath keeps the Unix socket path under the platform limit.
func shortSocketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "gmcli-dt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "devtools.sock")
}

func TestDevToolsRelayForwardsHTTPAndIsPrivate(t *testing.T) {
	upstream := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "127.0.0.1" {
			http.Error(w, "host rejected", http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true,"path":"`+r.URL.Path+`"}`)
	})}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = upstream.Serve(listener) }()
	defer upstream.Close()
	port := listener.Addr().(*net.TCPAddr).Port

	path := shortSocketPath(t)
	relay, err := newDevToolsRelay(path, port)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.Close()

	info, err := os.Stat(path)
	if err != nil || info.Mode().Type() != os.ModeSocket || info.Mode().Perm() != 0o600 {
		t.Fatalf("socket stat: mode=%v err=%v", info.Mode(), err)
	}

	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "GET /json/version HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"/json/version"`) {
		t.Fatalf("relayed response: %d %s", resp.StatusCode, body)
	}

	if err := relay.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("socket file remains after Close")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := relay.waitClosed(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestDevToolsRelayReplacesStaleSocketButNotFiles(t *testing.T) {
	path := shortSocketPath(t)
	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	stale.Close()
	// net.Listen's Close removes the file on some platforms; recreate a stale socket inode.
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		l, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		// Keep the inode but stop serving: close the fd without unlinking.
		rawConn, _ := l.(*net.UnixListener)
		rawConn.SetUnlinkOnClose(false)
		rawConn.Close()
	}
	relay, err := newDevToolsRelay(path, 1)
	if err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	relay.Close()

	regular := filepath.Join(filepath.Dir(path), "regular")
	if err := os.WriteFile(regular, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newDevToolsRelay(regular, 1); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("regular file was accepted: %v", err)
	}
	if data, _ := os.ReadFile(regular); string(data) != "keep" {
		t.Fatal("regular file was damaged")
	}
}
