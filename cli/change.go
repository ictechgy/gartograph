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
	"unicode/utf8"

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
	Origin    changeOrigin `json:"origin"`
	Workspace string       `json:"workspace"`
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
	modes  map[string]string
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
	for _, pattern := range opts.Patterns {
		if (pattern != "." && !strings.HasPrefix(pattern, "./")) || !filepath.IsLocal(filepath.FromSlash(pattern)) {
			return nil, fmt.Errorf("patch patterns must be repository-relative within --dir (such as . or ./...); absolute paths, file= queries and external package patterns cannot prove captured input")
		}
	}
	start := time.Now()
	repo, err := runGit(opts.Dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("locate Git repository: %w", err)
	}
	repo = filepath.Clean(filepath.FromSlash(strings.TrimSpace(repo)))
	// Git과 호출자가 같은 디렉터리를 /private/var와 /var 등 다른 별칭으로 고를 수 있다.
	// 캡처 안 원문 경로는 그대로 두고, 선택한 디렉터리만 물리 경로로 비교한다.
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return nil, fmt.Errorf("resolve Git worktree directory: %w", err)
	}
	requested, err := filepath.Abs(opts.Dir)
	if err != nil {
		return nil, err
	}
	requested, err = filepath.EvalSymlinks(requested)
	if err != nil {
		return nil, fmt.Errorf("resolve analysis directory: %w", err)
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
	// 내용은 같아도 실행 비트 등 Git 파일 모드가 달라진 patch를 누락하지 않는다.
	oldFiles, newFiles := changedCaptureFiles(before, after, subdir)
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
	return &changeReport{Mode: "patch", Complete: false, Before: changeSnapshot{before.origin, capturedWorkspace(before, subdir), beforeSide}, After: changeSnapshot{after.origin, capturedWorkspace(after, subdir), afterSide},
		Scope:             map[string]any{"directory": filepath.ToSlash(subdir), "patterns": patterns, "level": "symbol", "tests": opts.Tests, "dependencies": opts.IncludeDeps, "tags": opts.Tags, "goos": goos, "goarch": goarch, "captureMaxBytes": changeCaptureBytes, "captureMaxFiles": changeCaptureFiles, "loaderEnvironment": map[string]string{"GOENV": "off", "GOFLAGS": "", "GO111MODULE": "on", "GOPACKAGESDRIVER": "off", "GOWORK": "per-snapshot workspace"}},
		PreparationMillis: preparation, AnalysisMillis: time.Since(startedAnalysis).Milliseconds(), Limitations: []string{
			"before and after candidates are separate traversals; paths must never be joined across snapshots",
			"content hashes cover captured repository files, not the Go toolchain, environment, module cache or remote dependencies",
			"patch loading disables persistent GOENV, inherited GOFLAGS and external package drivers; pass build tags with --tags; workspace is the nearest captured go.work or off",
			"working-tree capture includes tracked and non-ignored untracked regular files; ignored/generated files and submodules are not reconstructed",
			"rename is represented as old-path removal and new-path addition; file seeds are conservative, not changed-line precision",
			"changed paths outside --dir remain relative to that directory (including ../); unmapped module, workspace and configuration inputs require review",
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
	modes := map[string]string{}
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
		if _, exists := modes[name]; exists {
			return nil, fmt.Errorf("duplicate Git source path %q", name)
		}
		names = append(names, name)
		ids = append(ids, fields[2])
		modes[name] = fields[0]
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
	return captured(files, modes, "git-commit", commit), nil
}

func captureWorkingTree(repo string) (*capturedSource, error) {
	res, err := readWorkingFiles(repo)
	if err != nil {
		return nil, err
	}
	again, err := readWorkingFiles(repo)
	if err != nil {
		return nil, err
	}
	if res.origin.ContentHash != again.origin.ContentHash {
		return nil, fmt.Errorf("working tree changed during capture; retry after edits finish")
	}
	return res, nil
}

func readWorkingFiles(repo string) (*capturedSource, error) {
	listed, err := runGit(repo, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, fmt.Errorf("list working files: %w", err)
	}
	indexModes, trustModes, err := workingFileModes(repo)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	modes := map[string]string{}
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
		mode := "100644"
		if trustModes && info.Mode().Perm()&0100 != 0 {
			mode = "100755"
		} else if !trustModes && indexModes[name] != "" {
			mode = indexModes[name]
		}
		modes[name] = mode
	}
	return captured(files, modes, "working-tree", ""), nil
}

// workingFileModes는 Git이 실행 비트를 신뢰하지 않는 파일시스템에서 index 모드를 보존한다.
func workingFileModes(repo string) (map[string]string, bool, error) {
	trust, err := runGit(repo, "config", "--type=bool", "--default", "true", "--get", "core.filemode")
	if err != nil {
		return nil, false, fmt.Errorf("read Git file mode policy: %w", err)
	}
	entries, err := runGit(repo, "ls-files", "--stage", "-z")
	if err != nil {
		return nil, false, fmt.Errorf("read index file modes: %w", err)
	}
	modes := map[string]string{}
	for _, entry := range strings.Split(entries, "\x00") {
		if entry == "" {
			continue
		}
		meta, name, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[2] != "0" {
			return nil, false, fmt.Errorf("cannot capture an unmerged index; resolve conflicts before analysis")
		}
		if fields[0] != "100644" && fields[0] != "100755" {
			return nil, false, fmt.Errorf("cannot capture non-regular index entry %q; prepare regular source files", name)
		}
		modes[name] = fields[0]
	}
	return modes, strings.TrimSpace(trust) == "true", nil
}

func capturePath(name string) error {
	if !utf8.ValidString(name) || !filepath.IsLocal(filepath.FromSlash(name)) || strings.ContainsAny(name, "\\:") || strings.Contains(name, "\x00") {
		return fmt.Errorf("unsafe or non-portable source path %q", name)
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Errorf("noncanonical source path %q", name)
		}
		if strings.EqualFold(part, ".git") {
			return fmt.Errorf("Git metadata is not source: %q", name)
		}
	}
	return nil
}

func captured(files map[string][]byte, modes map[string]string, kind, revision string) *capturedSource {
	return &capturedSource{files: files, modes: modes, origin: changeOrigin{Kind: kind, Revision: revision, ContentHash: captureHash(files, modes), Freshness: "captured-content", FileCount: len(files)}}
}

func captureHash(files map[string][]byte, modes map[string]string) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		sum := sha256.Sum256(files[name])
		fmt.Fprintf(hash, "%d:%s:%s:%x\n", len(name), name, modes[name], sum)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func changedCaptureFiles(before, after *capturedSource, subdir string) ([]string, []string) {
	names := map[string]bool{}
	for name := range before.files {
		names[name] = true
	}
	for name := range after.files {
		names[name] = true
	}
	var oldFiles, newFiles []string
	for name := range names {
		old, oldOK := before.files[name]
		current, newOK := after.files[name]
		if oldOK && newOK && bytes.Equal(old, current) && before.modes[name] == after.modes[name] {
			continue
		}
		relative, err := filepath.Rel(subdir, filepath.FromSlash(name))
		if err != nil {
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
	if err := materializeCapture(dir, capture); err != nil {
		return nil, err
	}
	if err = source.ValidateCapture(dir); err != nil {
		return nil, err
	}
	opts.Dir = filepath.Join(dir, subdir)
	opts.Level = graph.LevelSymbol
	opts.Captured = true
	if workspace := capturedWorkspace(capture, subdir); workspace != "off" {
		opts.CapturedWorkspace = filepath.Join(dir, filepath.FromSlash(workspace))
	} else {
		opts.CapturedWorkspace = ""
	}
	doc, err := loadDoc(&opts, "")
	if err != nil {
		return nil, err
	}
	return analysis.ChangeCandidates(doc, files, depth, maxEntries), nil
}

// 임시 root는 새로 만든 전용 디렉터리다. 그 안에서도 이름별 FS 신원이 겹치면 원문을 덮지 않는다.
func materializeCapture(dir string, capture *capturedSource) error {
	names := make([]string, 0, len(capture.files))
	for name := range capture.files {
		names = append(names, name)
	}
	sort.Strings(names)
	seen := map[string]bool{}
	directories := map[string]bool{"": true}
	for _, name := range names {
		if err := capturePath(name); err != nil {
			return err
		}
		key := name
		if runtime.GOOS == "windows" {
			key = strings.ToLower(name)
		}
		if seen[key] {
			return fmt.Errorf("source paths collide on this platform: %q", name)
		}
		seen[key] = true
		parts := strings.Split(name, "/")
		for depth := 1; depth < len(parts); depth++ {
			prefix := strings.Join(parts[:depth], "/")
			if directories[prefix] {
				continue
			}
			// 다른 원문 prefix가 기존 디렉터리를 가리키면 case/Unicode 별칭이 겹친 것이다.
			if err := os.Mkdir(filepath.Join(dir, filepath.FromSlash(prefix)), 0700); err != nil {
				if os.IsExist(err) {
					return fmt.Errorf("source paths collide on this platform: %q", prefix)
				}
				return err
			}
			directories[prefix] = true
		}
		file, err := os.OpenFile(filepath.Join(dir, filepath.FromSlash(name)), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			if os.IsExist(err) {
				return fmt.Errorf("source paths collide on this platform: %q", name)
			}
			return err
		}
		data := capture.files[name]
		n, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if n != len(data) {
			return io.ErrShortWrite
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// capturedWorkspace는 임시 디렉터리 밖 상위 workspace를 찾지 않는다.
func capturedWorkspace(capture *capturedSource, subdir string) string {
	for {
		file := filepath.ToSlash(filepath.Join(subdir, "go.work"))
		if _, ok := capture.files[file]; ok {
			return file
		}
		if subdir == "." {
			return "off"
		}
		subdir = filepath.Dir(subdir)
	}
}
