package main

import (
	"allocationcore/internal/corecli"
	"allocationcore/internal/demo"
	"allocationcore/internal/explorer"
	"allocationcore/internal/multi"
	"allocationcore/internal/review"
	"allocationcore/internal/rulekit"
	"allocationcore/internal/studio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if len(os.Args) < 2 {
		studio.Run()
		return
	}
	switch os.Args[1] {
	case "run", "validate", "explain", "doctor", "inspect", "compare", "trace", "vault", "batch", "serve-core", "capacity":
		if os.Args[1] == "serve-core" {
			os.Args[1] = "serve"
		}
		corecli.Run()
	case "core":
		os.Args = append([]string{os.Args[0]}, os.Args[2:]...)
		corecli.Run()
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
		fmt.Println("Allocation Core Clean / v2")
	default:
		fmt.Fprintf(os.Stderr, "Unknown command %q. Use 'serve', 'core', 'multi', 'rule', 'explorer', 'review' or double-click the application.\n", os.Args[1])
		os.Exit(2)
	}
}
