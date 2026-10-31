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
func streamMappedOnboardingAmounts(ctx context.Context, source io.Reader, dest string, precision int, dimensions []string, template string, mapping map[string]string) (onboardPreview, []string, error) {
	report := onboardPreview{Precision: precision, Template: template}
	columns, err := checkAmountMapping(mapping, dimensions)
	if err != nil {
		return report, nil, err
	}
	destFile, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return report, nil, err
	}
	defer destFile.Close()

	rawHash, normalizedHash := sha256.New(), sha256.New()
	in := csv.NewReader(bufio.NewReaderSize(io.TeeReader(source, rawHash), 256<<10))
	in.FieldsPerRecord = -1
	header, err := in.Read()
	if err != nil {
		return report, nil, fmt.Errorf("source CSV header: %w", err)
	}
	if len(header) > 0 {
		header[0] = strings.TrimPrefix(header[0], "\ufeff")
	}
	indexes := map[string]int{}
	for i, name := range header {
		if name == "" {
			return report, nil, errors.New("source CSV header has an empty column")
		}
		if _, duplicate := indexes[name]; duplicate {
			return report, nil, fmt.Errorf("duplicate source CSV column %q", name)
		}
		indexes[name] = i
	}
	for _, field := range columns {
		if _, ok := indexes[mapping[field]]; !ok {
			return report, nil, fmt.Errorf("source CSV has no column %q mapped to %q", mapping[field], field)
		}
	}
	out := csv.NewWriter(io.MultiWriter(destFile, normalizedHash))
	if err := out.Write(columns); err != nil {
		return report, nil, err
	}
	total := new(big.Int)
	accountIndex := indexOfAmountColumn(columns, "account")
	accounts := map[string]struct{}{}
	for {
		if e := ctx.Err(); e != nil {
			return report, nil, e
		}
		record, e := in.Read()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return report, nil, fmt.Errorf("source CSV row %d: %w", report.Rows+2, e)
		}
		if len(record) != len(header) {
			return report, nil, fmt.Errorf("source CSV row %d: expected %d columns, got %d", report.Rows+2, len(header), len(record))
		}
		normalized := make([]string, len(columns))
		for i, field := range columns {
			value := strings.TrimSpace(record[indexes[mapping[field]]])
			if value == "" {
				return report, nil, fmt.Errorf("source CSV row %d: empty %q", report.Rows+2, field)
			}
			if field == "account" && (len(value) > 256 || strings.ContainsAny(value, "\x00\r\n")) {
				return report, nil, fmt.Errorf("source CSV row %d: invalid account", report.Rows+2)
			}
			if field == "amount" {
				amount, err := engine.ParseScaled(value, precision)
				if err != nil {
					return report, nil, fmt.Errorf("source CSV row %d: invalid amount: %w", report.Rows+2, err)
				}
				total.Add(total, big.NewInt(amount))
			}
			normalized[i] = value
		}
		account := normalized[accountIndex]
		accounts[account] = struct{}{}
		if len(accounts) > maxOnboardingAccounts {
			return report, nil, fmt.Errorf("more than %d distinct accounts", maxOnboardingAccounts)
		}
		if err := out.Write(normalized); err != nil {
			return report, nil, err
		}
		report.Rows++
	}
	if report.Rows == 0 {
		return report, nil, errors.New("source CSV contains no amount rows")
	}
	out.Flush()
	if err = out.Error(); err != nil {
		return report, nil, err
	}
	if err = destFile.Sync(); err != nil {
		return report, nil, err
	}
	st, err := destFile.Stat()
	if err != nil {
		return report, nil, err
	}
	report.Bytes = st.Size()
	report.SHA256 = hex.EncodeToString(normalizedHash.Sum(nil))
	report.SourceSHA256 = hex.EncodeToString(rawHash.Sum(nil))
	report.Accounts = len(accounts)
	report.Total = formatMappedImportTotal(total, precision)
	names := make([]string, 0, len(accounts))
	for account := range accounts {
		names = append(names, account)
	}
	sort.Strings(names)
	return report, names, nil
}

func indexOfAmountColumn(columns []string, field string) int {
	for i, c := range columns {
		if c == field {
			return i
		}
	}
	// Unreachable when called on validated dimensions containing account.
	return -1
}

func formatMappedImportTotal(total *big.Int, precision int) string {
	signed := new(big.Int).Set(total)
	negative := signed.Sign() < 0
	signed.Abs(signed)
	unit := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(precision)), nil)
	whole, frac := new(big.Int).QuoRem(signed, unit, new(big.Int))
	result := whole.String()
	if precision != 0 {
		result = fmt.Sprintf("%s.%0*s", result, precision, frac.String())
	}
	if negative {
		result = "-" + result
	}
	return result
}
