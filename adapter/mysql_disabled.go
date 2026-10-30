//go:build !mysql

package adapter

import (
	"context"
	"fmt"
)

// MySQL support needs the official driver linked into the native executable.
// The default offline build remains dependency-free, but fails explicitly.
func ImportMySQLConfig(ctx context.Context, configPath, destination string) (Report, error) {
	return Report{}, fmt.Errorf("MySQL connector is not linked into this build; build with -tags mysql using the official github.com/go-sql-driver/mysql dependency (or download the MySQL-enabled CI release)")
}
func ImportMySQLBundleConfig(ctx context.Context, configPath, dest string) (BundleReport, error) {
	return BundleReport{}, fmt.Errorf("MySQL connector is not linked into this build; build with -tags mysql")
}
