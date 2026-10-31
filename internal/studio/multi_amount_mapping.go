package studio

// Source-column mapping for business amount onboarding. Financial formulas stay
// in Core: this transforms the *header contract* only and publishes the output
// exclusively inside a newly created project after all rows are validated.
import (
	"allocationcore/internal/engine"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"sort"
	"strings"
)

// sourceMapping is canonical column -> exact source CSV header. The optional
// row_id is only persisted when explicitly mapped. Unknown fields, duplicate
// sources and fabricated/empty dimensions are rejected, not inferred.
func checkAmountMapping(mapping map[string]string, dimensions []string) ([]string, error) {
	if len(mapping) == 0 {
		return nil, errors.New("source column mapping must not be empty")
	}
	fields := append(append([]string{}, dimensions...), "amount")
	accountDim := false
	for _, d := range dimensions {
		if d == "account" {
			accountDim = true
		}
	}
	if !accountDim {
		return nil, errors.New("rule dimensions must include account")
	}
	allowed := map[string]bool{"row_id": true}
	for _, k := range fields {
		allowed[k] = true
	}
	seen := map[string]bool{}
	for k, source := range mapping {
		if !allowed[k] || source == "" || strings.TrimSpace(source) != source || seen[source] {
			return nil, fmt.Errorf("invalid or ambiguous column mapping %q -> %q", k, source)
		}
		seen[source] = true
	}
	for _, k := range fields {
		if mapping[k] == "" {
			return nil, fmt.Errorf("missing source mapping for required field %q", k)
		}
	}
	if mapping["row_id"] != "" {
		fields = append(fields, "row_id")
	}
	return fields, nil
}

// A separate stream is needed because the existing unmodified-column route
// deliberately preserves the input CSV byte-for-byte for audit compatibility.
// This route writes only explicit normalized columns, never the raw source CSV.
func streamMappedOnboardingAmounts(ctx context.Context, source io.Reader, dest string, precision int, dimensions []string, templat