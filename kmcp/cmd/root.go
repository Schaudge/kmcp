// Copyright © 2020-2022 Wei Shen <shenwei356@gmail.com>
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package cmd

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
)

// RootCmd represents the base command when called without any subcommands
var RootCmd = &cobra.Command{
	Use:   "kmcp",
	Short: "K-mer-based Metagenomic Classification and Profilling",
	Long: fmt.Sprintf(`
    Program: kmcp (K-mer-based Metagenomic Classification and Profiling)
    Version: v%s
  Documents: https://bioinf.shenwei.me/kmcp
Source code: https://github.com/shenwei356/kmcp

KMCP is a tool for metagenomic classification and profiling.

KMCP can also be used for:
  1. Fast sequence search against large scales of genomic datasets
     as BIGSI and COBS do.
  2. Fast assembly/genome similarity estimation as Mash and sourmash do,
     by utilizing Minimizer, FracMinHash (Scaled MinHash), or Closed Syncmers.

`, VERSION),
}

// Execute adds all child commands to the root command sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(-1)
	}
}

func init() {

	defaultThreads := runtime.NumCPU()

	RootCmd.PersistentFlags().IntP("threads", "j", defaultThreads,
		formatFlagUsage("Number of CPUs cores to use."))

	// RootCmd.PersistentFlags().BoolP("verbose", "", false, "print verbose information (recommended)")

	RootCmd.PersistentFlags().BoolP("quiet", "q", false,
		formatFlagUsage("Do not print any verbose information. But you can write them to file with --log."))

	RootCmd.PersistentFlags().StringP("infile-list", "i", "",
		formatFlagUsage("File of input files list (one file per line). If given, they are appended to files from CLI arguments."))

	RootCmd.PersistentFlags().StringP("log", "", "", formatFlagUsage("Log file."))

	RootCmd.CompletionOptions.DisableDefaultCmd = true

	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})

	RootCmd.SetUsageTemplate(usageTemplate)
}

func formatFlagUsage(s string) string {
	// return s
	// return s + "\n"
	// return "· " + s
	// return "> " + s
	return "► " + s
	// return "▶ " + s
	// return "| " + s
	// return "  " + s
}

var usageTemplate = `Usage:{{if .Runnable}}
  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [command]{{end}}{{if gt (len .Aliases) 0}}

Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Flags:
{{.LocalFlags.FlagUsagesWrapped 110 | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Global Flags:
{{.InheritedFlags.FlagUsagesWrapped 110 | trimTrailingWhitespaces}}{{end}}{{if .HasHelpSubCommands}}

Additional help topics:{{range .Commands}}{{if .IsAdditionalHelpTopicCommand}}
  {{rpad .CommandPath .CommandPathPadding}} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableSubCommands}}

Use "{{.CommandPath}} [command] --help" for more information about a command.{{end}}
`
