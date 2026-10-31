package studio

// Business acceptance workbench: upload a private reference CSV, then reopen
// prior reconciliation evidence and download full coordinate differences.
// Allocation Core arithmetic and the acceptance comparator are unchanged.
import (
	"allocationcore/internal/acceptance"
	"allocationcore/internal/engine"
	"bufio"
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
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const maxReferenceUpload int64 = 256 << 20

var acceptanceDirectoryPattern = regexp.MustCompile(`^acceptance-[0-9]{8}T[0-9]{6}\.[0-9]{9}$`)
var multiJobIDPattern = regexp.MustCompile(`^[0-9a-f]{24}$`)

type referenceInfo struct {
	Exists bool   `json:"exists"`
	Bytes  int64  `json:"bytes,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

func referenceFingerprint(dir string) (referenceInfo, error) {
	path := filepath.Join(dir, "reference.csv")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return referenceInfo{}, nil
	}
	if err != nil {
		return referenceInfo{}, err
	}
	if !info.Mode().IsRegular() {
		return referenceInfo{}, errors.New("reference.csv must be an ordinary file (no links)")
	}
	f, err := os.Open(path)
	if err != nil {
		return referenceInfo{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return referenceInfo{}, err
	}
	return referenceInfo{Exists: true, Bytes: n, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

func (x *MultiApp) referenceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	dir, err := x.projectDir(r.URL.Query().Get("name"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	info, err := referenceFingerprint(dir)
	if err != nil {
		fail(w, 422, err)
		return
	}
	jsonOut(w, 200, info)
}

func projectReferenceContract(dir string) (int, []string, error) {
	assignments, err := readAssignments(filepath.Join(dir, "assignments.csv"))
	if err != nil {
		return 0, nil,