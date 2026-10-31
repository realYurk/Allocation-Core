package studio

// CSV-first project onboarding is a user-facing workflow, not a new financial
// engine. It makes a NEW project from an existing rule-template project, so
// importing data can never overwrite historical data or published results.
import (
	"allocationcore/internal/engine"
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
)

const maxOnboardingUpload = 256 << 20 // 256 MiB; larger files stay on local disk.
const maxOnboardingAccounts = 50000

// Clone only reusable rule/driver configuration, never source amounts,
// assignments, overrides, run history, or generated caches. This deliberately
// uses streaming copies to avoid retaining large driver files in Go heap.
func cloneBusinessAssets(source, stage string) error {
	for _, folder := range []string{"templates", "drivers"} {
		fromRoot := filepath.Join(source, folder)
		st, err := os.Lstat(fromRoot)
		if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("source %s folder is not a regular directory", folder)
		}
		if err := filepath.WalkDir(fromRoot, func(path string, item os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if item.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("source contains symlink: %s", item.Name())
			}
			rel, e := filepath.Rel(fromRoot, path)
			if e != nil {
				return e
			}
			out := filepath.Join(stage, folder, rel)
			if item.IsDir() {
				return os.MkdirAll(out, 0700)
			}
			if !item.Type().IsRegular() {
				return fmt.Errorf("source contains nonregular file: %s", item.Name())
			}
			in, e := os.Open(path)
			if e != nil {
				return e
			}
			f, e := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if e != nil {
				_ = in.Close()
				return e
			}
			_, e = io.Copy(f, in)
			if e == nil {
				e = f.Sync()
			}
			ce := f.Close()
			ie := in.Close()
			if e != nil {
				return e
			}
			if ce != nil {
				return ce
	