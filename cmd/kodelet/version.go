package main

import (
	"fmt"
	"os"

	"github.com/jingkaihe/kodelet/pkg/version"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version information",
	Long:  `Print the version information of Kodelet in JSON format.`,
	Run: func(cmd *cobra.Command, args []string) {
		info := version.Get()
		json, err := info.JSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error formatting version info: %s\n", err)
			os.Exit(1)
		}
		fmt.Println(json)
	},
}
