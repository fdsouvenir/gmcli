package browserauth

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// xvfbStartTimeout bounds how long we wait for Xvfb to report its display.
const xvfbStartTimeout = 10 * time.Second

// display describes where the sign-in browser will draw.
type display struct {
	// env entries to add to the browser process, for example DISPLAY=:7.
	env []string
	// virtual is true when gmcli started an Xvfb server of its own.
	virtual bool
	stop    func()
}

// hasDesktop reports whether the environment already provides a display.
func hasDesktop() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// ensureDisplay returns the existing desktop display, or starts a private
// virtual X server when allowVirtual is set and Xvfb is installed. Google
// refuses sign-in from Chrome's headless mode (it reports navigator.webdriver
// and a headless fingerprint), so a server without a desktop needs a real,
// if invisible, X display instead.
func ensureDisplay(ctx context.Context, allowVirtual bool, lookPath func(string) (string, error), start func(ctx context.Context, path string) (*exec.Cmd, error)) (*display, error) {
	if hasDesktop() {
		return &display{stop: func() {}}, nil
	}
	if !allowVirtual {
		return nil, codedErrorf(CodeNoDisplay, "browser sign-in needs a desktop display; run gmcli auth on your desktop, use --devtools-socket with a viewer on this server, or use --cookies-file - for piped input (see gmcli auth --help)")
	}
	path, err := lookPath("Xvfb")
	if err != nil {
		return nil, codedErrorf(CodeNoDisplay, "no desktop display and Xvfb is not installed; install xvfb (Debian: apt install xvfb) or run gmcli auth where a display exists")
	}
	return startXvfb(ctx, path, start)
}

// startXvfb runs Xvfb with -displayfd so the server chooses a free display
// number and reports it on a pipe once it is ready to accept connections.
func startXvfb(ctx context.Context, path string, start func(ctx context.Context, path string) (*exec.Cmd, error)) (*display, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return nil, codedErrorf(CodeBrowserFailed, "start virtual display: %w", err)
	}
	defer r.Close()
	cmd, err := start(ctx, path)
	if err != nil {
		w.Close()
		return nil, err
	}
	// ExtraFiles[0] becomes fd 3 in the child.
	cmd.ExtraFiles = []*os.File{w}
	cmd.Args = append(cmd.Args, "-displayfd", "3")
	if err := cmd.Start(); err != nil {
		w.Close()
		return nil, codedErrorf(CodeBrowserFailed, "start virtual display: %w", err)
	}
	w.Close() // the child holds its own copy; closing ours lets reads see EOF if it exits
	stop := func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	}

	type result struct {
		number string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		line, err := bufio.NewReader(r).ReadString('\n')
		done <- result{number: strings.TrimSpace(line), err: err}
	}()
	select {
	case res := <-done:
		if res.err != nil || res.number == "" {
			stop()
			return nil, codedErrorf(CodeBrowserFailed, "virtual display did not start")
		}
		return &display{env: []string{"DISPLAY=:" + res.number}, virtual: true, stop: stop}, nil
	case <-time.After(xvfbStartTimeout):
		stop()
		return nil, codedErrorf(CodeBrowserFailed, "virtual display did not start within %s", xvfbStartTimeout)
	case <-ctx.Done():
		stop()
		return nil, ctx.Err()
	}
}

// defaultXvfb builds the Xvfb command. The server listens on its Unix socket
// only, with a screen large enough for Google's sign-in pages.
func defaultXvfb(ctx context.Context, path string) (*exec.Cmd, error) {
	if path == "" {
		return nil, errors.New("empty Xvfb path")
	}
	cmd := exec.CommandContext(ctx, path, "-screen", "0", "1024x900x24", "-nolisten", "tcp")
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd, nil
}
