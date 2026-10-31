package studio

// Studio snapshots are deliberately independent of the allocation engine.
// Every run consumes one coherent, private snapshot rather than live editable
// rule and data files. The input manifest is retained for later external system comparison.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type InputFingerprint struct {
	File   string `json:"file"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

func snapshotLegacyCase(ctx context.Context, original, destParent string) (dest string, ruleBytes []byte, manifest []InputFingerprint, err error) {
	if resolvedOriginal, e := filepath.EvalSymlinks(original); e == nil {
		original = resolvedOriginal
	}
	// The caller has already called readCase, but repeat validation against a
	// fresh filesystem view to avoid trusting any old browser-side state.
	raw, err := os.ReadFile(filepath.Join(original, "rule.json"))
	if err != nil {
		return
	}
	var rule GenericRule
	if err = json.Unmarshal(raw, &rule); err != nil {
		return
	}
	if len(rule.Stages) == 0 {
		err = errors.New("empty stages")
		return
	}
	names := map[string]bool{"rule.json": true}
	for _, files := range []map[string]string{rule.Datasets.Amounts, rule.Datasets.Drivers} {
		for _, rel := range files {
			if rel == "" || filepath.IsAbs(rel) || strings.Contains(rel, "\\") || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
				err = fmt.Errorf("unsafe dataset name: %q", rel)
				return
			}
			names[rel] = true
		}
	}
	dest, err = os.MkdirTemp(destParent, ".studio-input-")
	if err != nil {
		return
	}
	defer func() {
		if err != nil {
			os.RemoveAll(dest)
		}
	}()
	all := make([]string, 0, len(names))
	for name := range names {
		all = append(all, name)
	}
	sort.Strings(all)
	for _, rel := range all {
		if err = ctx.Err(); err != nil {
			return
		}
		src := filepath.Join(original, rel)
		resolved, e := filepath.EvalSymlinks(src)
		if e != nil {
			err = e
			return
		}
		within, e := filepath.Rel(original, resolved)
		if e != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
			err = fmt.Errorf("dataset escapes project: %s", rel)
			return
		}
		fi, e := os.Stat(src)
		if e != nil || !fi.Mode().IsRegular() {
			err = fmt.Errorf("source is not a regular file: %s", rel)
			return
		}
		target := filepath.Join(dest, rel)
		if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return
		}
		var in *os.File
		in, err = os.Open(src)
		if err != nil {
			return
		}
		var out *os.File
		out, err = os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			in.Close()
			return
		}
		hash := sha256.New()
		var n int64
		n, err = io.Copy(io.MultiWriter(out, hash), &contextReader{ctx: ctx, reader: in})
		if e := out.Sync(); err == nil {
			err = e
		}
		if e := out.Close(); err == nil {
			err = e
		}
		in.Close()
		if err != nil {
			return
		}
		copyHash := hex.EncodeToString(hash.Sum(nil))
		// The final full reread detects edits which occur during file copy.
		// Unlike an extra reread of the copy, the inline copy hash is free.
		var sourceHash string
		sourceHash, err = fileHashStudio(src)
		if err != nil {
			return
		}
		if sourceHash != copyHash {
			err = fmt.Errorf("input changed while copying: %s", rel)
			return
		}
		manifest = append(manifest, InputFingerprint{rel, n, copyHash})
		if rel == "rule.json" {
			if copyHash != hex.EncodeToString(sha256Sum(raw)) {
				err = errors.New("rule changed during snapshot")
				return
			}
			ruleBytes = raw
		}
	}
	return
}
func sha256Sum(b []byte) []byte { h := sha256.Sum256(b); return h[:] }
func fileHashStudio(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// contextReader permits cancellation during multi-GB snapshot copies.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > 256*1024 {
		p = p[:256*1024]
	}
	return r.reader.Read(p)
}
