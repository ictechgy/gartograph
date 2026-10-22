package cli

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ictechgy/gartograph/analysis"
	"github.com/ictechgy/gartograph/graph"
	"github.com/ictechgy/gartograph/source"
)

// 소스 캡처의 준비 비용도 제한한다. 넘으면 부분 입력을 성공처럼 분석하지 않는다.
const changeCaptureBytes = 128 << 20
const changeCaptureFiles = 20000

type changeOrigin struct {
	Kind        string `json:"kind"`
	Revision    string `json:"revision,omitempty"`
	ContentHash string `json:"contentHash"`
	Freshness   string `json:"freshness"`
	FileCount   int    `json:"fileCount"`
}

type changeSnapshot struct {
	Origin changeOrigin `json:"origin"`
	*analysis.ChangeSide
}

type changeReport struct {
	Mode              string         `json:"mode"`
	Complete          bool           `json:"complete"`
	Before            changeSnapshot `json:"before"`
	After             changeSnapshot `json:"after"`
	Scope             map[string]any `json:"scope"`
	PreparationMillis int64          `json:"preparationMillis"`
	AnalysisMillis    int64          `json:"analysisMillis"`
	Limitations       []string       `json:"limitations"`
}

type capturedSource struct {
	files  map[string][]byte
	origin changeOrigin
}

// runChangeImpact는 CLI와 MCP가 같은 입력 캡처·분석 계약을 쓰는 진입점이다.
func runChangeImpact(opts source.Options, base, head string, depth, maxEntries int) (*changeReport, error) {
	if depth < 0 || maxEntries < 0 {
		return nil, fmt.Errorf("patch depth and max must be non-negative")
	}
	if base == "" {
		return nil, fmt.Errorf("patch impact needs a base revision")
	}
	start := time.Now()
	repo, err := runGit(opts.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("locate Git repository: %w", err)
	}
	repo = filepath.Clean(filepath.FromSlash(strings.TrimSpace(repo)))
	requested, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	subdir, err := filepath.Rel(repo, requested)
	if err != nil || (subdir != "." && !filepath.IsLocal(subdir)) {
		return nil, fmt.Errorf("analysis directory must be inside the Git worktree")
	}
	before, err := captureRevision(repo, base)
	if err != nil {
		return nil, fmt.Errorf("capture base: %w", err)
	}
	var after *capturedSource
	if head != "" {
		after, err = captureRevision(repo, head)
	} else {
		after, err = captureWorkingTree(repo)
	}
	if err != nil {
		return nil, fmt.Errorf("capture head: %w", err)
	}
	oldFiles, newFiles := changedCaptureFiles(before.files, after.files, subdir)
	preparation := time.Since(start).Milliseconds()
	startedAnalysis := time.Now()
	beforeSide, err := analyzeCapture(before, subdir, opts, oldFiles, depth, maxEntries)
	if err != nil {
		return nil, fmt.Errorf("analyze base: %w", err)
	}
	afterSide, err := analyzeCapture(after, subdir, opts, newFiles, depth, maxEntries)
	if err != nil {
		return nil, fmt.Errorf("analyze head: %w", err)
	}
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	goos, goarch := opts.GOOS, opts.GOARCH
	if goos == "" {
		goos = os.Getenv("GOOS")
		if goos == "" {
			goos = runtime.GOOS
		}
	}
	if goarch == "" {
		goarch = os.Getenv("GOARCH")
		if goarch == "" {
			goarch = runtime.GOARCH
		}
	}
	return &changeReport{Mode: "patch", Complete: false, Before: changeSnapshot{before.origin, beforeSide}, After: changeSnapshot{after.origin, afterSide},
		Scope:             map[string]any{"directory": filepath.ToSlash(subdir), "patterns": patterns, "level": "symbol", "tests": opts.Tests, "dependencies": opts.IncludeDeps, "tags": opts.Tags, "goos": goos, "goarch": goarch, "captureMaxBytes": changeCaptureBytes, "captureMaxFiles": changeCaptureFiles},
		PreparationMillis: preparation, AnalysisMillis: time.Since(startedAnalysis).Milliseconds(), Limitations: []string{
			"before and after candidates are separate traversals; paths must never be joined across snapshots",
			"content hashes cover captured repository files, not the Go toolchain, environment, module cache or remote dependencies",
			"working-tree capture includes tracked and non-ignored untracked regular files; ignored/generated files and submodules are not reconstructed",
			"rename is represented as old-path removal and new-path addition; file seeds are conservative, not changed-line precision",
			"HTTP and DB data contracts, runtime configuration and callback registration are not fully assessed; no candidates does not mean safe",
		}}, nil
}

func captureRevision(repo, rev string) (*capturedSource, error) {
	if err := checkSinceRevision(rev); err != nil {
		return nil, err
	}
	commit, err := runGit(repo, "rev-parse", "--verify", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return nil, fmt.Errorf("resolve %q as one commit: %w", rev, err)
	}
	commit = strings.TrimSpace(commit)
	if !objectIDPattern.MatchString(commit) {
		return nil, fmt.Errorf("invalid resolved commit")
	}
	tree, err := runGit(repo, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return nil, fmt.Errorf("read commit tree: %w", err)
	}
	var names, ids []string
	for _, entry := range strings.Split(tree, "\x00") {
		if entry == "" {
			continue
		}
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return nil, fmt.Errorf("cannot capture non-regular Git entry %q (symlinks/submodules need explicit source preparation)", name)
		}
		if err := capturePath(name); err != nil {
			return nil, err
		}
		names = append(names, name)
		ids = append(ids, fields[2])
	}
	if len(names) > changeCaptureFiles {
		return nil, fmt.Errorf("source capture exceeds %d files", changeCaptureFiles)
	}
	cmd, err := gitCommand(repo, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	cmd.Stdin = strings.NewReader(strings.Join(ids, "\n") + "\n")
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errs bytes.Buffer
	cmd.Stderr = &errs
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	finished := false
	defer func() {
		if !finished {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(pipe)
	files := map[string][]byte{}
	total := 0
	for i, name := range names {
		header, readErr := reader.ReadString('\n')
		if readErr != nil {
			return nil, fmt.Errorf("read Git blob header: %w", readErr)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 || fields[0] != ids[i] || fields[1] != "blob" {
			return nil, fmt.Errorf("missing or invalid Git blob for %s", name)
		}
		size, parseErr := strconv.Atoi(fields[2])
		if parseErr != nil || size < 0 || size > changeCaptureBytes-total {
			return nil, fmt.Errorf("source capture exceeds %d bytes", changeCaptureBytes)
		}
		data := make([]byte, size)
		if _, err = io.ReadFull(reader, data); err != nil {
			return nil, err
		}
		term, readErr := reader.ReadByte()
		if readErr != nil || term != '\n' {
			return nil, fmt.Errorf("invalid Git blob terminator")
		}
		files[name] = data
		total += size
	}
	if err = cmd.Wait(); err != nil {
		finished = true
		return nil, fmt.Errorf("read Git blobs: %w: %s", err, errs.String())
	}
	finished = true
	return captured(files, "git-commit", commit), nil
}

func captureWorkingTree(repo string) (*capturedSource, error) {
	files, err := readWorkingFiles(repo)
	if err != nil {
		return nil, err
	}
	res := captured(files, "working-tree", "")
	again, err := readWorkingFiles(repo)
	if err != nil {
		return nil, err
	}
	if res.origin.ContentHash != captureHash(again) {
		return nil, fmt.Errorf("working tree changed during capture; retry after edits finish")
	}
	return res, nil
}

func readWorkingFiles(repo string) (map[string][]byte, error) {
	listed, err := runGit(repo, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("list working files: %w", err)
	}
	files := map[string][]byte{}
	total := 0
	for _, name := range strings.Split(listed, "\x00") {
		if name == "" {
			continue
		}
		if _, ok := files[name]; ok {
			continue
		}
		if err := capturePath(name); err != nil {
			return nil, err
		}
		full := filepath.Join(repo, filepath.FromSlash(name))
		// 상위 경로의 링크도 거부한다. 작업 트리 밖의 내용을 몰래 섞으면 해시의 범위가 틀린다.
		for part := full; part != repo; part = filepath.Dir(part) {
			if filepath.Dir(part) == part {
				return nil, fmt.Errorf("source path escaped repository: %s", name)
			}
			info, statErr := os.Lstat(part)
			if os.IsNotExist(statErr) {
				break
			}
			if statErr != nil {
				return nil, statErr
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("cannot capture symlink %s; prepare regular source files", name)
			}
		}
		info, statErr := os.Lstat(full)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			return nil, statErr
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("cannot capture non-regular source %s", name)
		}
		if len(files) >= changeCaptureFiles || info.Size() > int64(changeCaptureBytes-total) {
			return nil, fmt.Errorf("source capture exceeds %d files or %d bytes", changeCaptureFiles, changeCaptureBytes)
		}
		data, readErr := source.ReadCaptureFile(full, int64(changeCaptureBytes-total))
		if readErr != nil {
			return nil, readErr
		}
		if len(data) > changeCaptureBytes-total {
			return nil, fmt.Errorf("source capture exceeds %d bytes", changeCaptureBytes)
		}
		total += len(data)
		files[name] = data
	}
	return files, nil
}

func capturePath(name string) error {
	if !filepath.IsLocal(filepath.FromSlash(name)) || strings.ContainsAny(name, "\\:") || strings.Contains(name, "\x00") {
		return fmt.Errorf("unsafe or non-portable source path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf("Git metadata is not source: %q", name)
		}
	}
	return nil
}

func captured(files map[string][]byte, kind, revision string) *capturedSource {
	return &capturedSource{files: files, origin: changeOrigin{Kind: kind, Revision: revision, ContentHash: captureHash(files), Freshness: "captured-content", FileCount: len(files)}}
}

func captureHash(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(hash, "%d:%s:%x\n", len(name), name, sum)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func changedCaptureFiles(before, after map[string][]byte, subdir string) ([]string, []string) {
	names := map[string]bool{}
	for name := range before {
		names[name] = true
	}
	for name := range after {
		names[name] = true
	}
	var oldFiles, newFiles []string
	for name := range names {
		old, oldOK := before[name]
		current, newOK := after[name]
		if oldOK && newOK && bytes.Equal(old, current) {
			continue
		}
		relative, err := filepath.Rel(subdir, filepath.FromSlash(name))
		if err != nil || !filepath.IsLocal(relative) {
			continue
		}
		if oldOK {
			oldFiles = append(oldFiles, filepath.ToSlash(relative))
		}
		if newOK {
			newFiles = append(newFiles, filepath.ToSlash(relative))
		}
	}
	sort.Strings(oldFiles)
	sort.Strings(newFiles)
	return oldFiles, newFiles
}

func analyzeCapture(capture *capturedSource, subdir string, opts source.Options, files []string, depth, maxEntries int) (*analysis.ChangeSide, error) {
	dir, err := os.MkdirTemp("", "gartograph-change-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	seen := map[string]bool{}
	for name, data := range capture.files {
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToLower(name)
		}
		if seen[key] {
			return nil, fmt.Errorf("source paths collide on this platform: %s", name)
		}
		seen[key] = true
		full := filepath.Join(dir, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			return nil, err
		}
		if err = os.WriteFile(full, data, 0600); err != nil {
			return nil, err
		}
	}
	if err = source.ValidateCapture(dir); err != nil {
		return nil, err
	}
	opts.Dir = filepath.Join(dir, subdir)
	opts.Level = graph.LevelSymbol
	opts.Captured = true
	doc, err := loadDoc(&opts, "")
	if err != nil {
		return nil, err
	}
	return analysis.ChangeCandidates(doc, files, depth, maxEntries), nil
}
