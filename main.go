package main

import (
	"allocationcore/adapter"
	"allocationcore/internal/acceptance"
	"allocationcore/internal/corecli"
	"allocationcore/internal/demo"
	"allocationcore/internal/explorer"
	"allocationcore/internal/multi"
	"allocationcore/internal/profiling"
	"allocationcore/internal/review"
	"allocationcore/internal/rulekit"
	"allocationcore/internal/studio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
)

// Build metadata is set by CI; source builds show unreleased/local status.
var buildVersion = "unreleased"
var buildCommit = "unknown"

func versionInfo() string {
	commit := buildCommit
	if commit == "unknown" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					commit = setting.Value
				}
			}
		}
	}
	connector := "not linked"
	if adapter.MySQLDriverLinked() {
		connector = "linked (enterprise connection not verified)"
	}
	return fmt.Sprintf("Allocation Core %s\nRevision: %s\nMySQL driver: %s", buildVersion, commit, connector)
}

func main() {
	if len(os.Args) < 2 {
		studio.Run()
		return
	}
	switch os.Args[1] {
	case "data":
		// Data mappings never connect to databases implicitly. External SQL and
		// Cube connectors can use the same ingest.Source Go API.
		if len(os.Args) != 6 || (os.Args[2] != "csv" && os.Args[2] != "mysql" && os.Args[2] != "mysql-bundle") || os.Args[3] != "--config" {
			fmt.Fprintln(os.Stderr, "usage: AllocationCore data csv|mysql|mysql-bundle --config mapping.json OUTPUT.csv_OR_DIR")
			os.Exit(2)
		}
		var r adapter.Report
		var e error
		if os.Args[2] == "mysql-bundle" {
			result, err := adapter.ImportMySQLBundleConfig(context.Background(), os.Args[4], os.Args[5])
			if err != nil {
				fmt.Fprintln(os.Stderr, "Data import failed:", err)
				os.Exit(1)
			}
			b, _ := json.MarshalIndent(result, "", "  ")
			fmt.Println(string(b))
			return
		} else if os.Args[2] == "mysql" {
			r, e = adapter.ImportMySQLConfig(context.Background(), os.Args[4], os.Args[5])
		} else {
			r, e = adapter.ImportCSVConfig(context.Background(), os.Args[4], os.Args[5])
		}
		if e != nil {
			fmt.Fprintln(os.Stderr, "Data import failed:", e)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		fmt.Println(string(b))
	case "profile":
		if e := profiling.CLI(os.Args[2:]); e != nil {
			fmt.Fprintln(os.Stderr, "Profiling failed:", e)
			os.Exit(1)
		}
	case "reconcile":
		acceptance.CLI()
	case "run", "validate", "explain", "doctor", "inspect", "compare", "trace", "vault", "batch", "serve-core", "capacity":
		if os.Args[1] == "serve-core" {
			os.Args[1] = "serve"
		}
		corecli.Run()
	case "core":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		corecli.Run()
	case "multi-worker":
		multi.WorkerCLI()
	case "multi":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		multi.CLI()
	case "rule":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		rulekit.CLI()
	case "explorer":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		explorer.CLI()
	case "review":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		review.CLI()
	case "serve":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		studio.Run()
	case "-root", "-data-dir", "-listen", "-no-browser":
		studio.Run()
	case "demo":
		root, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(os.Args) >= 3 {
			root, err = filepath.Abs(os.Args[2])
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
		}
		n, err := demo.Generate(root)
		if err == nil && len(os.Args) >= 4 {
			count, e := strconv.Atoi(os.Args[3])
			if e != nil {
				err = e
			} else {
				err = demo.GenerateScale(root, count)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "Synthetic demo generation failed:", err)
			os.Exit(1)
		}
		fmt.Printf("Synthetic example files generated: %d (local only)\n", n)
	case "version", "--version":
		fmt.Println(versionInfo())
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q. Use 'serve', 'core', 'multi', 'rule', 'explorer', 'data', 'profile', 'review' or double-click the application.\n", os.Args[1])
		os.Exit(2)
	}
}
