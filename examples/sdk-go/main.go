// An integration example: all paths are chosen by the calling application.
package main

import (
	"allocationcore/sdk"
	"fmt"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 3 {
		log.Fatal("usage: sdk-go RULE.json NEW_OUTPUT_DIRECTORY")
	}
	report, err := sdk.Inspect(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	if report.Status != "PASS" {
		for _, check := range report.Checks {
			if check.Error != "" {
				fmt.Fprintln(os.Stderr, check.Dataset, check.Kind, check.Error)
			}
		}
		log.Fatal("input inspection blocked")
	}
	result, err := sdk.Run(sdk.RunRequest{RulePath: os.Args[1], OutputDir: os.Args[2], Workers: 2})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("Core status=%s input=%s output=%s\n", result.Status, result.InputAmount, result.OutputAmount)
	fmt.Println("Input inspection and conservation do not replace independent business reconciliation.")
}
