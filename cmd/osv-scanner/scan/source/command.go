package source

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/osv-scanner/v2/cmd/osv-scanner/internal/helper"
	"github.com/google/osv-scanner/v2/pkg/models"
	"github.com/google/osv-scanner/v2/pkg/osvscanner"
	"github.com/urfave/cli/v2"
)

var projectScanFlags = []cli.Flag{
	&cli.StringSliceFlag{
		Name:      "lockfile",
		Aliases:   []string{"L"},
		Usage:     "scan package lockfile on this path",
		TakesFile: true,
	},
	&cli.StringSliceFlag{
		Name:      "sbom",
		Aliases:   []string{"S"},
		Usage:     "scan sbom file on this path, the sbom file name must follow the relevant spec",
		TakesFile: true,
	},
	&cli.BoolFlag{
		Name:    "recursive",
		Aliases: []string{"r"},
		Usage:   "check subdirectories",
		Value:   false,
	},
	&cli.BoolFlag{
		Name:  "no-ignore",
		Usage: "also scan files that would be ignored by .gitignore",
		Value: false,
	},
	&cli.StringSliceFlag{
		Name:  "call-analysis",
		Usage: "attempt call analysis on code to detect only active vulnerabilities",
	},
	&cli.StringSliceFlag{
		Name:  "no-call-analysis",
		Usage: "disables call graph analysis",
	},
	&cli.BoolFlag{
		Name:  "include-git-root",
		Usage: "include scanning git root (non-submoduled) repositories",
		Value: false,
	},
	&cli.StringFlag{
		Name:  "data-source",
		Usage: "source to fetch package information from; value can be: deps.dev, native",
		Value: "deps.dev",
		Action: func(_ *cli.Context, s string) error {
			if s != "deps.dev" && s != "native" {
				return fmt.Errorf("unsupported data-source \"%s\" - must be one of: deps.dev, native", s)
			}

			return nil
		},
	},
	&cli.StringFlag{
		Name:  "maven-registry",
		Usage: "URL of the default registry to fetch Maven metadata",
	},
}

func Command(stdout, stderr io.Writer) *cli.Command {
	return &cli.Command{
		Name:        "source",
		Usage:       "scans a source project's dependencies for known vulnerabilities using the OSV database.",
		Description: "scans a source project's dependencies for known vulnerabilities using the OSV database.",
		Flags:       append(projectScanFlags, helper.GetScanGlobalFlags()...),
		ArgsUsage:   "[directory1 directory2...]",
		Action: func(c *cli.Context) error {
			return action(c, stdout, stderr)
		},
	}
}

func action(context *cli.Context, stdout, stderr io.Writer) error {
	format := context.String("format")

	outputPath := context.String("output")
	serve := context.Bool("serve")
	if serve {
		format = "html"
		if outputPath == "" {
			// Create a temporary directory
			tmpDir, err := os.MkdirTemp("", "osv-scanner-result")
			if err != nil {
				return fmt.Errorf("failed creating temporary directory: %w\n"+
					"Please use `--output result.html` to specify the output path", err)
			}

			// Remove the created temporary directory after
			defer os.RemoveAll(tmpDir)
			outputPath = filepath.Join(tmpDir, "index.html")
		}
	}

	scanLicensesAllowlist, err := helper.GetScanLicensesAllowlist(context)
	if err != nil {
		return err
	}

	callAnalysisStates := helper.CreateCallAnalysisStates(context.StringSlice("call-analysis"), context.StringSlice("no-call-analysis"))

	experimentalScannerActions := helper.GetExperimentalScannerActions(context, scanLicensesAllowlist)
	// Add `source` specific experimental configs
	experimentalScannerActions.TransitiveScanningActions = osvscanner.TransitiveScanningActions{
		Disabled:         context.Bool("no-resolve"),
		NativeDataSource: context.String("data-source") == "native",
		MavenRegistry:    context.String("maven-registry"),
	}

	scannerAction := osvscanner.ScannerActions{
		LockfilePaths:              context.StringSlice("lockfile"),
		SBOMPaths:                  context.StringSlice("sbom"),
		Recursive:                  context.Bool("recursive"),
		IncludeGitRoot:             context.Bool("include-git-root"),
		NoIgnore:                   context.Bool("no-ignore"),
		ConfigOverridePath:         context.String("config"),
		DirectoryPaths:             context.Args().Slice(),
		CallAnalysisStates:         callAnalysisStates,
		ExperimentalScannerActions: experimentalScannerActions,
	}

	var vulnResult models.VulnerabilityResults
	vulnResult, err = osvscanner.DoScan(scannerAction)

	if err != nil && !errors.Is(err, osvscanner.ErrVulnerabilitiesFound) {
		return err
	}

	if errPrint := helper.PrintResult(stdout, stderr, outputPath, format, &vulnResult); errPrint != nil {
		return fmt.Errorf("failed to write output: %w", errPrint)
	}

	// Auto-open outputted HTML file for users.
	if outputPath != "" {
		if serve {
			helper.ServeHTML(outputPath)
		} else if format == "html" {
			slog.Info("HTML output available at: " + outputPath)
		}
	}

	// This may be nil.
	return err
}
