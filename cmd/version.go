package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version is the gmcli release tag.
// Override at link time with `-ldflags "-X github.com/fdsouvenir/gmcli/cmd.Version=vX.Y.Z"`.
var Version = "v0.1.1-alpha"

const licenseNotice = "gmcli is licensed under GNU AGPL-3.0. " +
	"It depends on libgm from mautrix/gmessages (AGPL-3.0, " +
	"Copyright (C) Tulir Asokan and contributors). " +
	"See LICENSE and NOTICE in the source tree for full text."

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Built   string `json:"built,omitempty"`
	GoVer   string `json:"go_version"`
	License string `json:"license"`
	Notice  string `json:"notice"`
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version, build info, and license notice",
		RunE: func(cmd *cobra.Command, args []string) error {
			info := buildInfo()
			if flags.jsonOut {
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(info)
			}
			fmt.Printf("gmcli %s\n", info.Version)
			if info.Commit != "" {
				fmt.Printf("  commit:  %s\n", info.Commit)
			}
			if info.Built != "" {
				fmt.Printf("  built:   %s\n", info.Built)
			}
			fmt.Printf("  go:      %s\n", info.GoVer)
			fmt.Printf("  license: %s\n", info.License)
			fmt.Println()
			fmt.Println(licenseNotice)
			return nil
		},
	}
}

func buildInfo() versionInfo {
	info := versionInfo{
		Version: Version,
		License: "AGPL-3.0-or-later",
		Notice:  licenseNotice,
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		info.GoVer = bi.GoVersion
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				info.Commit = s.Value
			case "vcs.time":
				info.Built = s.Value
			}
		}
	}
	return info
}
