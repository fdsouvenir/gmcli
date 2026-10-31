package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestGaiaTerminalGuard(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		human, json, in, out, err, ok bool
	}{
		{"human", true, false, true, true, true, true},
		{"agent-default", false, false, true, true, true, false},
		{"json", true, true, true, true, true, false},
		{"pipe-in", true, false, false, true, true, false},
		{"pipe-out", true, false, true, false, true, false},
		{"pipe-error", true, false, true, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := gaiaTerminalGuard(tc.human, tc.json, tc.in, tc.out, tc.err)
			if (err == nil) != tc.ok {
				t.Fatal(err)
			}
		})
	}
	c := authGaiaCmd()
	if err := c.Args(c, []string{"must-not-accept-secret-argument"}); err == nil {
		t.Fatal("accepted credential argument")
	}
}

func TestGaiaHumanInterruptTerminatesBlockedStartup(t *testing.T) {
	if os.Getenv("GMCLI_TEST_GAIA_SIGNAL") == "1" {
		_, cancel := gaiaContext()
		defer cancel()
		fmt.Println("ready")
		time.Sleep(time.Hour) // simulate upstream's uncancellable WaitGroup; no auth/network
		return
	}
	child := exec.Command(os.Args[0], "-test.run=^TestGaiaHumanInterruptTerminatesBlockedStartup$")
	child.Env = append(os.Environ(), "GMCLI_TEST_GAIA_SIGNAL=1")
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	defer child.Process.Kill()
	ready := make(chan bool, 1)
	go func() { scanner := bufio.NewScanner(stdout); ready <- scanner.Scan() && scanner.Text() == "ready" }()
	select {
	case ok := <-ready:
		if !ok {
			t.Fatal("helper not ready")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("helper startup timed out")
	}
	if err = child.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("SIGINT did not terminate helper")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SIGINT trapped while startup blocked")
	}
}

func TestAuthParentRejectsUnknownArguments(t *testing.T) {
	c := authCmd()
	if err := c.Args(c, []string{"gaia-typo"}); err == nil {
		t.Fatal("unknown subcommand would fall through to QR pairing")
	}
}
