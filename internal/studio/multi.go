package studio

// Multi-account Studio is strictly local. It never implements financial formulas;
// it invokes allocation_multi, which in turn runs the authoritative alloc binary.
import (
	"allocationcore/internal/multi"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type MultiApp struct {
	parent  *Application
	root    string
	program string
	mu      sync.Mutex
	jobs    map[string]*Job
	busy    map[string]bool
	slots   chan struct{}
	sem     chan struct{}
	cancels map[string]context.CancelFunc
}

var multiSafe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,48}$`)
var multiCSV = regexp.MustCompile(`^[A-Za-z0-9_-]+\.csv$`)

func (a *Application) registerMulti(m *http.ServeMux) *MultiApp {
	program, err := os.Executable()
	if err != nil {
		panic(err)
	}
	x := &MultiApp{parent: a, root: filepath.Join(a.data, "multi"), program: program, jobs: map[string]*Job{}, busy: map[string]bool{}, slots: make(chan struct{}, 8), sem: make(chan struct{}, 2), cancels: map[string]context.CancelFunc{}}
	if err := os.MkdirAll(x.root, 0700); err != nil {
		panic(err)
	}
	recovered, err := recoverJournals(filepath.Join(a.data, "multi-jobs"), true)
	if err != nil {
		panic(fmt.Errorf("multi job recovery: %w", err))
	}
	x.jobs = recovered
	m.HandleFunc("/multi", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			w.WriteHeader(405)
			return
		}
		b, e := publicFS.ReadFile("public/multi.html")
		if e != nil {
			fail(w, 500, e)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(b)
	})
	m.HandleFunc("/api/multi/projects", x.projects)
	m.HandleFunc("/api/multi/create", x.create)
	m.HandleFunc("/api/multi/onboard", x.createFromAmountsCSV)
	m.HandleFunc("/api/multi/visual-draft", x.createVisualDraft)
	m.HandleFunc("/api/multi/driver-needs", x.driverNeeds)
	m.HandleFunc("/api/multi/driver-import", x.importMappedDriver)
	m.HandleFunc("/api/m