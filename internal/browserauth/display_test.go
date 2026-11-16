package browserauth

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"testing"
)

func TestEnsureDisplayUsesExistingDesktop(t *testing.T) {
	t.Setenv("DISPLAY", ":9")
	t.Setenv("WAYLAND_DISPLAY", "")
	disp, err := ensureDisplay(context.Background(), true, func(string) (string, error) {
		t.Fatal("looked for Xvfb although DISPLAY is set")
		return "", nil
	}, nil)
	if err != nil || disp.virtual || len(disp.env) != 0 {
		t.Fatalf("disp=%+v err=%v", disp, err)
	}
	disp.stop()
}

func TestEnsureDisplayWithoutDesktop(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("display detection is Linux-only")
	}
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")

	t.Run("virtual display not allowed", func(t *testing.T) {
		_, err := ensureDisplay(context.Background(), false, nil, nil)
		var coded *Error
		if !errors.As(err, &coded) || coded.Code() != CodeNoDisplay {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("Xvfb missing", func(t *testing.T) {
		_, err := ensureDisplay(context.Background(), true, func(string) (string, error) { return "", errors.New("nope") }, nil)
		var coded *Error
		if !errors.As(err, &coded) || coded.Code() != CodeNoDisplay || !contains(err.Error(), "Xvfb") {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("fake Xvfb reports its display on fd 3", func(t *testing.T) {
		disp, err := ensureDisplay(context.Background(), true,
			func(string) (string, error) { return "/bin/sh", nil },
			func(ctx context.Context, _ string) (*exec.Cmd, error) {
				return exec.CommandContext(ctx, "/bin/sh", "-c", "echo 42 >&3; exec sleep 30"), nil
			})
		if err != nil {
			t.Fatal(err)
		}
		defer disp.stop()
		if !disp.virtual || len(disp.env) != 1 || disp.env[0] != "DISPLAY=:42" {
			t.Fatalf("disp=%+v", disp)
		}
	})
	t.Run("fake Xvfb exits without a display", func(t *testing.T) {
		_, err := ensureDisplay(context.Background(), true,
			func(string) (string, error) { return "/bin/sh", nil },
			func(ctx context.Context, _ string) (*exec.Cmd, error) {
				return exec.CommandContext(ctx, "/bin/sh", "-c", "exit 1"), nil
			})
		var coded *Error
		if !errors.As(err, &coded) || coded.Code() != CodeBrowserFailed {
			t.Fatalf("err=%v", err)
		}
	})
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
