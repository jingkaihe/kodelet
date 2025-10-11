package main

import (
	"fmt"

	"github.com/jingkaihe/kodelet/pkg/llmstxt"
	"github.com/spf13/cobra"
)

var llmstxtCmd = &cobra.Command{
	Use:   "llms.txt",
	Short: "Display LLM-friendly usage guide",
	Long:  `Display the llms.txt file which contains LLM-friendly documentation about kodelet usage.`,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Print(llmstxt.GetContent())
	},
}
