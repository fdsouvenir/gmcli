// gmcli — local-first Google Messages CLI.
//
// Copyright (C) 2026 Fred Souvenir.
// Licensed under the GNU AGPL-3.0-or-later. See LICENSE for details.
package main

import (
	"fmt"
	"os"

	"github.com/fdsouvenir/gmcli/cmd"
)

func main() {
	if len(os.Args) == 2 {
		switch os.Args[1] {
		case "-v", "-V", "--version":
			fmt.Println(cmd.CurrentVersion())
			return
		}
	}
	if err := cmd.Root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "gmcli:", err)
		os.Exit(cmd.ExitCode(err))
	}
}
