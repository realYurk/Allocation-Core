package studio

// Allocation Studio is an independent local inspection and launch UI.
// It never reimplements or changes financial allocation calculations.

import (
	"allocationcore/internal/demo"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed public/*
var publicFS embed.FS

type Application struct {
	home, data, custom, output, engine, addr string
	mu                                       sync.RWMutex
	jobs                                     map[string]*Job
	sem                                      chan struct{}
	slots                                    chan struct{}
	cancels                                  map[string]context.CancelFunc
	editMu                                   sync.Mutex
}
type Job struct {
	ID     string `json:"id"`
	Case   string `json:"case"`
	Status string `json:"status"`
	Start  string `json:"start"`
	End    string `json:"end,omitempty"`
	Error  string `json:"error,omitempty"`
	Output string `json:"-"`
	Log    string `json:"log,omitempty"`
}
type GenericRule struct {
	Name       string   `json:"name"`
	Dimensions []string `json:"dimensions"`
	Datasets   struct {
		Amounts map[string]string `json:"amounts"`
		Drivers map[string]string `json:"drivers"`
	} `json:"datasets"`
	Stages    []map[string]any `json:"stages"`
	Precision int              `json:"precision"`
}
type CaseSummary struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"`
	Kind         string `json:"kind"`
	Stages       int    `json:"stages"`
	Drivers      int    `json:"drivers"`
	AccountCount int    `json:"accountCount"`
	Period       string `json:"period"`
	Amount       string `json:"amount"`
}

// Only these local-only business import endpoints accept multipart file da