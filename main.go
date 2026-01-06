// google-photos-immich-urls maps Google Photos URLs from Google Takeout archives
// to their corresponding Immich asset URLs.
//
// This tool is designed to help users who have migrated from Google Photos to Immich
// update their external references (like Obsidian notes) to point to the new Immich URLs.
//
// License: AGPL-3.0 (compatible with immich-go for potential future integration)
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/thedirtyfew/google-photos-immich-urls/internal/mapper"
)

var (
	// CLI flags
	server     string
	apiKey     string
	skipSSL    bool
	dryRun     bool
	outputFile string
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "google-photos-immich-urls [flags] <takeout-zip-files...>",
	Short: "Map Google Photos URLs to Immich URLs",
	Long: `Maps Google Photos URLs from Google Takeout ZIP archives to their corresponding
Immich asset URLs using SHA1 hash matching.

This tool reads Google Takeout ZIP files directly without extracting them,
finds the Google Photos URLs in the JSON metadata files, and matches them
to assets in your Immich server by computing and comparing file hashes.

Example:
  google-photos-immich-urls -s https://immich.example.com -k YOUR_API_KEY takeout-*.zip

The output is a JSON file containing the URL mappings that can be used
for find/replace operations in your notes or other documents.`,
	Args: cobra.MinimumNArgs(1),
	RunE: run,
}

func init() {
	rootCmd.Flags().StringVarP(&server, "server", "s", "", "Immich server address (e.g., https://immich.example.com)")
	rootCmd.Flags().StringVarP(&apiKey, "api-key", "k", "", "Immich API key")
	rootCmd.Flags().BoolVar(&skipSSL, "skip-verify-ssl", false, "Skip SSL certificate verification")
	rootCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Don't connect to Immich, just list found URLs")
	rootCmd.Flags().StringVarP(&outputFile, "output", "o", "", "Output file path (default: stdout)")

	rootCmd.MarkFlagRequired("server")
	rootCmd.MarkFlagRequired("api-key")
}

func run(cmd *cobra.Command, args []string) error {
	// Setup context with cancellation on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "\nInterrupted, shutting down...")
		cancel()
	}()

	// Validate flags
	if !dryRun {
		if server == "" {
			return fmt.Errorf("--server is required (unless using --dry-run)")
		}
		if apiKey == "" {
			return fmt.Errorf("--api-key is required (unless using --dry-run)")
		}
	}

	// Create mapper
	m, err := mapper.New(mapper.Config{
		Server:       server,
		APIKey:       apiKey,
		SkipSSL:      skipSSL,
		DryRun:       dryRun,
		TakeoutPaths: args,
		Logger: func(format string, args ...interface{}) {
			fmt.Fprintf(os.Stderr, format+"\n", args...)
		},
	})
	if err != nil {
		return err
	}
	defer m.Close()

	// Run mapping
	fmt.Fprintln(os.Stderr, "Processing takeout files...")
	result, err := m.Run(ctx)
	if err != nil {
		return err
	}

	// Output results
	var out *os.File
	if outputFile != "" {
		out, err = os.Create(outputFile)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer out.Close()
	} else {
		out = os.Stdout
	}

	if err := result.WriteJSON(out); err != nil {
		return fmt.Errorf("failed to write output: %w", err)
	}

	// Print summary to stderr
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "=== Summary ===")
	fmt.Fprintf(os.Stderr, "Total JSON files processed: %d\n", result.Stats.TotalJSONFiles)
	fmt.Fprintf(os.Stderr, "Google Photos URLs found:   %d\n", result.Stats.TotalGoogleURLs)
	fmt.Fprintf(os.Stderr, "Matched in Immich:          %d\n", result.Stats.Matched)
	fmt.Fprintf(os.Stderr, "  - by hash:                %d\n", result.Stats.MatchedByHash)
	fmt.Fprintf(os.Stderr, "  - by filename:            %d\n", result.Stats.MatchedByFilename)
	fmt.Fprintf(os.Stderr, "Not found in Immich:        %d\n", result.Stats.NotFoundInImmich)
	fmt.Fprintf(os.Stderr, "No media file for JSON:     %d\n", result.Stats.NoMediaFile)
	fmt.Fprintf(os.Stderr, "Hash computation errors:    %d\n", result.Stats.HashErrors)

	if outputFile != "" {
		fmt.Fprintf(os.Stderr, "\nOutput written to: %s\n", outputFile)
	}

	return nil
}
