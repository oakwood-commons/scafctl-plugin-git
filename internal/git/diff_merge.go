package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	sdkprovider "github.com/oakwood-commons/scafctl-plugin-sdk/provider"
)

const (
	minMergeTreeGitMajor  = 2
	minMergeTreeGitMinor  = 38
	minAttrSourceGitMajor = 2
	minAttrSourceGitMinor = 41

	internalGitAliasName      = "scafctl-internal-op"
	internalGitAliasEnvVar    = "SCAFCTL_GIT_INTERNAL_ALIAS"
	internalGitHooksDirEnvVar = "SCAFCTL_GIT_EMPTY_HOOKS"
)

var gitVersionOutputFunc = queryGitVersionOutput

// diffFileEntry represents per-file line change statistics parsed from git diff --numstat -z.
type diffFileEntry struct {
	Path      string
	OldPath   string
	Additions int
	Deletions int
	Binary    bool
}

// resolveRepoPath defaults empty path to "." and resolves relative paths against
// the logical working directory in ctx when set by the host (such as MCP or --cwd).
func resolveRepoPath(ctx context.Context, rawPath string) string {
	target := rawPath
	if target == "" {
		target = "."
	}
	if !filepath.IsAbs(target) {
		if wd, ok := sdkprovider.WorkingDirectoryFromContext(ctx); ok && wd != "" {
			return filepath.Join(wd, target)
		}
	}
	return target
}

// validateGitRef ensures a user-supplied revision specifier is a single revision
// and cannot inject git CLI flags, range/revision-set operators, whitespace, or control bytes.
func validateGitRef(field, ref string) error {
	if ref == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if strings.HasPrefix(ref, "-") {
		return fmt.Errorf("%s %q must not start with '-'", field, ref)
	}
	if strings.HasPrefix(ref, "^") ||
		strings.Contains(ref, "..") ||
		strings.Contains(ref, "^!") ||
		strings.Contains(ref, "^@") ||
		strings.Contains(ref, "^-") {
		return fmt.Errorf("%s %q must not contain range operator or revision-set syntax", field, ref)
	}
	for _, r := range ref {
		if r <= 0x20 || r == 0x7f {
			return fmt.Errorf("%s contains whitespace or control characters", field)
		}
	}
	return nil
}

func optionalStringInput(inputs map[string]any, field, defaultVal string) (string, error) {
	raw, exists := inputs[field]
	if !exists || raw == nil {
		return defaultVal, nil
	}
	val, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", field)
	}
	if val == "" {
		return defaultVal, nil
	}
	return val, nil
}

func validateRepoDir(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return fmt.Errorf("directory does not exist: %s", path)
	}
	if err != nil {
		return fmt.Errorf("checking directory %s: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}
	return nil
}

func executeDiff(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	rawPath, err := optionalStringInput(inputs, fieldPath, "")
	if err != nil {
		return nil, err
	}
	path := resolveRepoPath(ctx, rawPath)
	if err := validateRepoDir(path); err != nil {
		return nil, err
	}

	base, err := optionalStringInput(inputs, fieldBase, "")
	if err != nil {
		return nil, err
	}
	if base != "" {
		if err := validateGitRef(fieldBase, base); err != nil {
			return nil, err
		}
	}

	head, err := optionalStringInput(inputs, fieldHead, "")
	if err != nil {
		return nil, err
	}
	if head != "" {
		if err := validateGitRef(fieldHead, head); err != nil {
			return nil, err
		}
	}

	threeDot := true
	if rawThreeDot, exists := inputs[fieldThreeDot]; exists && rawThreeDot != nil {
		b, ok := rawThreeDot.(bool)
		if !ok {
			return nil, fmt.Errorf("%s must be a boolean", fieldThreeDot)
		}
		threeDot = b
	}

	emptyHooksDir, err := os.MkdirTemp("", ".scafctl-git-hooks-*")
	if err != nil {
		return nil, fmt.Errorf("creating temporary hooks directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(emptyHooksDir) }()

	stdout, stderr, runErr := executeDiffCommand(ctx, path, emptyHooksDir, base, head, threeDot)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("executing diff: %w", ctx.Err())
	}
	if runErr != nil {
		errMsg := strings.TrimSpace(string(stderr))
		if errMsg != "" {
			return nil, fmt.Errorf("computing diff: %s: %w", errMsg, runErr)
		}
		return nil, fmt.Errorf("computing diff: %w", runErr)
	}

	entries, formattedOutput, totalAdditions, totalDeletions, err := parseNumstatZ(stdout)
	if err != nil {
		return nil, fmt.Errorf("parsing diff output: %w", err)
	}

	files := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		item := map[string]any{
			"path":      entry.Path,
			"additions": entry.Additions,
			"deletions": entry.Deletions,
			"binary":    entry.Binary,
		}
		if entry.OldPath != "" {
			item["oldPath"] = entry.OldPath
		}
		files = append(files, item)
	}

	return &sdkprovider.Output{
		Data: map[string]any{
			"success":        true,
			"output":         formattedOutput,
			"error":          "",
			fieldOperation:   "diff",
			fieldPath:        path,
			fieldFiles:       files,
			"changedFiles":   len(files),
			"totalAdditions": totalAdditions,
			"totalDeletions": totalDeletions,
		},
	}, nil
}

// parseNumstatZ parses NUL-delimited git diff --numstat -z output.
// Standard records have the form "<add>\t<del>\t<path>\x00", whereas rename/copy
// records have an empty path after the second tab followed by two NUL-terminated
// paths: "<add>\t<del>\t\x00<oldPath>\x00<newPath>\x00".
func parseNumstatZ(raw []byte) (entries []diffFileEntry, formatted string, totalAdditions, totalDeletions int, err error) {
	entries = []diffFileEntry{}
	if len(raw) == 0 {
		return entries, "", 0, 0, nil
	}

	var lines []string
	pos := 0
	for pos < len(raw) {
		tab1 := bytes.IndexByte(raw[pos:], '\t')
		if tab1 < 0 {
			return nil, "", 0, 0, fmt.Errorf("unexpected numstat record without tab separator")
		}
		addStr := string(raw[pos : pos+tab1])
		pos += tab1 + 1

		tab2 := bytes.IndexByte(raw[pos:], '\t')
		if tab2 < 0 {
			return nil, "", 0, 0, fmt.Errorf("unexpected numstat record missing second tab separator")
		}
		delStr := string(raw[pos : pos+tab2])
		pos += tab2 + 1

		nul1 := bytes.IndexByte(raw[pos:], 0)
		if nul1 < 0 {
			return nil, "", 0, 0, fmt.Errorf("unterminated numstat path field")
		}
		firstPath := string(raw[pos : pos+nul1])
		pos += nul1 + 1

		var path, oldPath string
		if firstPath == "" {
			nul2 := bytes.IndexByte(raw[pos:], 0)
			if nul2 < 0 {
				return nil, "", 0, 0, fmt.Errorf("unterminated numstat rename source path")
			}
			oldPath = string(raw[pos : pos+nul2])
			pos += nul2 + 1

			nul3 := bytes.IndexByte(raw[pos:], 0)
			if nul3 < 0 {
				return nil, "", 0, 0, fmt.Errorf("unterminated numstat rename destination path")
			}
			path = string(raw[pos : pos+nul3])
			pos += nul3 + 1
		} else {
			path = firstPath
		}

		var additions, deletions int
		binary := false
		if addStr == "-" && delStr == "-" {
			binary = true
		} else {
			additions, err = strconv.Atoi(addStr)
			if err != nil {
				return nil, "", 0, 0, fmt.Errorf("invalid addition count %q for %q: %w", addStr, path, err)
			}
			deletions, err = strconv.Atoi(delStr)
			if err != nil {
				return nil, "", 0, 0, fmt.Errorf("invalid deletion count %q for %q: %w", delStr, path, err)
			}
			totalAdditions += additions
			totalDeletions += deletions
		}

		entries = append(entries, diffFileEntry{
			Path:      path,
			OldPath:   oldPath,
			Additions: additions,
			Deletions: deletions,
			Binary:    binary,
		})

		displayPath := path
		if oldPath != "" {
			displayPath = oldPath + " => " + path
		}
		lines = append(lines, fmt.Sprintf("%s\t%s\t%s", addStr, delStr, displayPath))
	}

	return entries, strings.Join(lines, "\n"), totalAdditions, totalDeletions, nil
}

func executeDiffCommand(ctx context.Context, path, emptyHooksDir, base, head string, threeDot bool) (stdout, stderr []byte, err error) {
	switch {
	case base != "":
		if head == "" {
			head = "HEAD"
		}
		wantType := "tree"
		sep := ".."
		if threeDot {
			wantType = "commit"
			sep = "..."
		}
		baseOID, baseStderr, baseErr := resolveRevisionOID(ctx, path, emptyHooksDir, base, wantType)
		if baseErr != nil {
			return nil, baseStderr, baseErr
		}
		headOID, headStderr, headErr := resolveRevisionOID(ctx, path, emptyHooksDir, head, wantType)
		if headErr != nil {
			return nil, headStderr, headErr
		}
		if fltErr := rejectExternalCommitDiffFilters(ctx, path, emptyHooksDir, baseOID, headOID, threeDot); fltErr != nil {
			return nil, nil, fltErr
		}
		aliasCmd := fmt.Sprintf("diff --no-ext-diff --no-textconv --submodule=short --numstat -M -z %s%s%s --", baseOID, sep, headOID)
		return runGitAlias(ctx, path, emptyHooksDir, aliasCmd, nil)

	case head != "":
		headOID, headStderr, headErr := resolveRevisionOID(ctx, path, emptyHooksDir, head, "tree")
		if headErr != nil {
			return nil, headStderr, headErr
		}
		return runWorkingTreeDiffAgainstOID(ctx, path, emptyHooksDir, headOID)

	default:
		headOID, headStderr, headErr := resolveRevisionOID(ctx, path, emptyHooksDir, "HEAD", "tree")
		if headErr == nil {
			return runWorkingTreeDiffAgainstOID(ctx, path, emptyHooksDir, headOID)
		}
		if ctx.Err() == nil && isUnbornHead(ctx, path, emptyHooksDir) {
			if emptyTree, treeErr := resolveEmptyTreeOID(ctx, path, emptyHooksDir); treeErr == nil {
				return runWorkingTreeDiffAgainstOID(ctx, path, emptyHooksDir, emptyTree)
			}
		}
		return nil, headStderr, headErr
	}
}

func runWorkingTreeDiffAgainstOID(ctx context.Context, path, emptyHooksDir, treeOID string) (stdout, stderr []byte, err error) {
	if fltErr := rejectExternalWorkingTreeDiffFilters(ctx, path, emptyHooksDir, treeOID); fltErr != nil {
		return nil, nil, fltErr
	}
	aliasCmd := fmt.Sprintf("diff --no-ext-diff --no-textconv --submodule=short --ignore-submodules=dirty --numstat -M -z %s --", treeOID)
	return runGitAlias(ctx, path, emptyHooksDir, aliasCmd, nil)
}

func isUnbornHead(ctx context.Context, path, emptyHooksDir string) bool {
	env := readOnlyGitEnv(emptyHooksDir, nil)
	insideCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "rev-parse", "--is-inside-work-tree")
	insideOut, _, insideErr := runPreparedGit(insideCmd, path, env)
	if insideErr != nil || strings.TrimSpace(string(insideOut)) != "true" {
		return false
	}
	headCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "rev-parse", "--verify", "--quiet", "HEAD")
	_, _, headErr := runPreparedGit(headCmd, path, env)
	var exitErr *exec.ExitError
	return errors.As(headErr, &exitErr) && exitErr.ExitCode() == 1
}

// resolveEmptyTreeOID computes the repository's empty tree object ID (SHA-1 or SHA-256)
// without writing anything to the object database.
func resolveEmptyTreeOID(ctx context.Context, path, emptyHooksDir string) (string, error) {
	env := readOnlyGitEnv(emptyHooksDir, nil)
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "hash-object", "-t", "tree", "--stdin")
	cmd.Stdin = bytes.NewReader(nil)
	out, _, err := runPreparedGit(cmd, path, env)
	if err != nil {
		return "", fmt.Errorf("computing empty tree OID: %w", err)
	}
	oid := strings.TrimSpace(string(out))
	if !isHexOID(oid) {
		return "", fmt.Errorf("unexpected empty tree OID %q", oid)
	}
	return oid, nil
}

func executeMergeCheck(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	rawPath, err := optionalStringInput(inputs, fieldPath, "")
	if err != nil {
		return nil, err
	}
	path := resolveRepoPath(ctx, rawPath)
	if err := validateRepoDir(path); err != nil {
		return nil, err
	}

	base, err := optionalStringInput(inputs, fieldBase, "")
	if err != nil {
		return nil, err
	}
	if base == "" {
		return nil, fmt.Errorf("%s is required for merge-check operation", fieldBase)
	}
	if err := validateGitRef(fieldBase, base); err != nil {
		return nil, err
	}

	head, err := optionalStringInput(inputs, fieldHead, "HEAD")
	if err != nil {
		return nil, err
	}
	if err := validateGitRef(fieldHead, head); err != nil {
		return nil, err
	}

	emptyHooksDir, err := os.MkdirTemp("", ".scafctl-git-hooks-*")
	if err != nil {
		return nil, fmt.Errorf("creating temporary hooks directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(emptyHooksDir) }()

	stdout, stderr, runErr := executeMergeTreeCommand(ctx, path, emptyHooksDir, base, head)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("executing merge-check: %w", ctx.Err())
	}
	clean := true
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) && exitErr.ExitCode() == 1 && len(stdout) > 0 {
			clean = false
		} else {
			if verErr := verifyGitVersionForMergeCheck(ctx); verErr != nil {
				return nil, verErr
			}
			stderrStr := strings.TrimSpace(string(stderr))
			if isUnsupportedMergeTreeFlagError(stderrStr) {
				return nil, fmt.Errorf("git merge-check requires Git %d.%d or newer: %w", minMergeTreeGitMajor, minMergeTreeGitMinor, runErr)
			}
			if stderrStr == "" {
				stderrStr = strings.TrimSpace(string(stdout))
			}
			if stderrStr != "" {
				return nil, fmt.Errorf("checking merge of %q into %q: %s: %w", head, base, stderrStr, runErr)
			}
			return nil, fmt.Errorf("checking merge of %q into %q: %w", head, base, runErr)
		}
	}

	treeOID, conflictedFiles, infoOutput, err := parseMergeTreeZ(stdout, clean)
	if err != nil {
		return nil, fmt.Errorf("parsing merge-tree output: %w", err)
	}

	return &sdkprovider.Output{
		Data: map[string]any{
			"success":         true,
			"output":          infoOutput,
			"error":           "",
			fieldOperation:    "merge-check",
			fieldPath:         path,
			"clean":           clean,
			"treeOid":         treeOID,
			"conflictedFiles": conflictedFiles,
		},
	}, nil
}

// parseMergeTreeZ parses NUL-delimited output from git merge-tree --write-tree -z.
// The first NUL-terminated field is always the resulting tree OID. When clean is
// false (exit code 1), conflicted stage records ("<mode> <oid> <stage>\t<path>")
// follow until an empty NUL-terminated separator, after which informational
// message tuples ("<num-paths>\0<path1>...\0<type>\0<msg>\0") appear.
func parseMergeTreeZ(stdout []byte, clean bool) (treeOID string, conflictedFiles []string, infoOutput string, err error) {
	if len(stdout) == 0 {
		return "", nil, "", fmt.Errorf("empty output from git merge-tree")
	}

	nul := bytes.IndexByte(stdout, 0)
	var rest []byte
	if nul < 0 {
		treeOID = strings.TrimSpace(string(stdout))
	} else {
		treeOID = strings.TrimSpace(string(stdout[:nul]))
		rest = stdout[nul+1:]
	}
	if treeOID == "" {
		return "", nil, "", fmt.Errorf("missing tree OID in git merge-tree output")
	}
	if !isHexOID(treeOID) {
		return "", nil, "", fmt.Errorf("invalid tree OID %q in git merge-tree output", treeOID)
	}

	conflictedFiles = []string{}
	if clean {
		msgs, _ := parseMergeTreeMessagesZ(splitNulTokens(rest))
		if len(msgs) > 0 {
			return treeOID, conflictedFiles, strings.Join(msgs, "\n"), nil
		}
		return treeOID, conflictedFiles, treeOID, nil
	}

	seen := make(map[string]struct{})
	var msgTokens []string
	inMessages := false

	for len(rest) > 0 {
		idx := bytes.IndexByte(rest, 0)
		var tok string
		if idx < 0 {
			tok = string(rest)
			rest = nil
		} else {
			tok = string(rest[:idx])
			rest = rest[idx+1:]
		}

		if !inMessages {
			if tok == "" {
				inMessages = true
				continue
			}
			if meta, filePath, ok := strings.Cut(tok, "\t"); ok && isConflictStageMeta(meta) {
				if filePath != "" {
					seen[filePath] = struct{}{}
				}
				continue
			}
			inMessages = true
		}

		msgTokens = append(msgTokens, tok)
	}

	messages, tupleConflictPaths := parseMergeTreeMessagesZ(msgTokens)
	for _, p := range tupleConflictPaths {
		if p != "" {
			seen[p] = struct{}{}
		}
	}

	for file := range seen {
		conflictedFiles = append(conflictedFiles, file)
	}
	slices.Sort(conflictedFiles)

	infoOutput = strings.Join(messages, "\n")
	if infoOutput == "" && len(conflictedFiles) > 0 {
		infoOutput = fmt.Sprintf("merge conflict in %s", strings.Join(conflictedFiles, ", "))
	}

	return "", conflictedFiles, infoOutput, nil
}

// splitNulTokens splits NUL-terminated records without leaving a trailing empty
// element after the final NUL terminator.
func splitNulTokens(raw []byte) []string {
	var tokens []string
	for len(raw) > 0 {
		idx := bytes.IndexByte(raw, 0)
		if idx < 0 {
			tokens = append(tokens, string(raw))
			break
		}
		tokens = append(tokens, string(raw[:idx]))
		raw = raw[idx+1:]
	}
	return tokens
}

// parseMergeTreeMessagesZ extracts human-readable messages (and conflict paths)
// from git merge-tree -z informational tuples:
// "<num-paths>\0<path1>\0...<pathN>\0<conflict-type>\0<conflict-message>\0".
func parseMergeTreeMessagesZ(tokens []string) (messages, conflictPaths []string) {
	for i := 0; i < len(tokens); {
		if tokens[i] == "" {
			i++
			continue
		}
		if n, err := strconv.Atoi(tokens[i]); err == nil && n >= 0 && i+n+3 <= len(tokens) {
			paths := tokens[i+1 : i+1+n]
			conflictType := tokens[i+1+n]
			msg := strings.TrimSpace(strings.TrimRight(tokens[i+n+2], "\r\n"))
			if msg != "" {
				messages = append(messages, msg)
			}
			if strings.HasPrefix(conflictType, "CONFLICT") {
				conflictPaths = append(conflictPaths, paths...)
			}
			i += n + 3
			continue
		}
		if msg := strings.TrimSpace(strings.TrimRight(tokens[i], "\r\n")); msg != "" {
			messages = append(messages, msg)
		}
		i++
	}
	return messages, conflictPaths
}

func isConflictStageMeta(meta string) bool {
	fields := strings.Fields(meta)
	if len(fields) != 3 {
		return false
	}
	switch fields[2] {
	case "1", "2", "3":
		return true
	default:
		return false
	}
}

func isUnsupportedMergeTreeFlagError(stderr string) bool {
	lower := strings.ToLower(stderr)
	if strings.Contains(lower, "unknown option") && strings.Contains(lower, "write-tree") {
		return true
	}
	return strings.Contains(lower, "usage: git merge-tree <base-tree>")
}

func verifyGitVersionForMergeCheck(ctx context.Context) error {
	out, err := gitVersionOutputFunc(ctx)
	if err != nil {
		return nil
	}
	return checkMinimumGitVersion(out, minMergeTreeGitMajor, minMergeTreeGitMinor)
}

func queryGitVersionOutput(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "git", "--version")
	out, _, err := runPreparedGit(cmd, "", nil)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func checkMinimumGitVersion(rawVersion string, minMajor, minMinor int) error {
	major, minor, display, ok := parseGitVersion(rawVersion)
	if !ok {
		return nil
	}
	if major < minMajor || (major == minMajor && minor < minMinor) {
		return fmt.Errorf("git merge-check requires Git %d.%d or newer (found %s)", minMajor, minMinor, display)
	}
	return nil
}

func parseGitVersion(raw string) (major, minor int, display string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "git version ")
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return 0, 0, "", false
	}
	verToken := fields[0]
	parts := strings.Split(verToken, ".")
	if len(parts) < 2 {
		return 0, 0, "", false
	}
	majVer, okMaj := parseLeadingInt(parts[0])
	minVer, okMin := parseLeadingInt(parts[1])
	if !okMaj || !okMin {
		return 0, 0, "", false
	}
	return majVer, minVer, verToken, true
}

func parseLeadingInt(s string) (int, bool) {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	v, err := strconv.Atoi(s[:end])
	if err != nil {
		return 0, false
	}
	return v, true
}

func executeMergeTreeCommand(ctx context.Context, path, emptyHooksDir, base, head string) (stdout, stderr []byte, err error) {
	baseOID, baseStderr, baseErr := resolveRevisionOID(ctx, path, emptyHooksDir, base, "commit")
	if baseErr != nil {
		return nil, baseStderr, baseErr
	}
	headOID, headStderr, headErr := resolveRevisionOID(ctx, path, emptyHooksDir, head, "commit")
	if headErr != nil {
		return nil, headStderr, headErr
	}

	if drvErr := rejectExternalMergeDriversAndFilters(ctx, path, emptyHooksDir, baseOID, headOID); drvErr != nil {
		return nil, nil, drvErr
	}

	aliasCmd := fmt.Sprintf("merge-tree --write-tree -z -- %s %s", headOID, baseOID)
	return runGitAlias(ctx, path, emptyHooksDir, aliasCmd, nil)
}

// rejectExternalMergeDriversAndFilters ensures read-only merge-check never executes
// external shell merge.<name>.driver commands or (when renormalization is enabled)
// external filter.<name>.(clean|smudge|process) commands. Built-in Git merge drivers
// (text, binary, union, -merge) and unused driver/filter config entries remain allowed.
func rejectExternalMergeDriversAndFilters(ctx context.Context, workDir, emptyHooksDir, baseOID, headOID string) error {
	drivers, err := listConfiguredExternalMergeDrivers(ctx, workDir, emptyHooksDir)
	if err != nil {
		return err
	}

	var filters map[string]struct{}
	if isMergeRenormalizeEnabled(ctx, workDir, emptyHooksDir) {
		filters, _, err = listConfiguredExternalFilters(ctx, workDir, emptyHooksDir, true)
		if err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(drivers) == 0 && len(filters) == 0 {
		return nil
	}

	var defaultDriver string
	if len(drivers) > 0 {
		if val, ok := getLastGitConfigValue(ctx, workDir, emptyHooksDir, "merge.default", false); ok && val != "" {
			defaultDriver = val
		}
	}

	touchedStdin, mergeBases, err := collectMergeTouchedPathsZ(ctx, workDir, emptyHooksDir, baseOID, headOID)
	if err != nil || len(touchedStdin) == 0 {
		return err
	}

	probeCtx, err := resolveProbeRepoContext(ctx, workDir, emptyHooksDir, false)
	if err != nil {
		return err
	}
	callerAttrSource := hasEffectiveCallerAttrSource(ctx)
	sourceTrees := append([]string{baseOID, headOID}, mergeBases...)

	if len(drivers) > 0 {
		checkDriverTokens := func(tokens []string) error {
			for i := 0; i+2 < len(tokens); i += 3 {
				rawInfo := tokens[i+2]
				if _, isRawExternal := drivers[rawInfo]; isRawExternal {
					return fmt.Errorf(
						"external merge driver %q is not supported in read-only merge-check; only built-in Git merge drivers are evaluated",
						rawInfo,
					)
				}
				if rawInfo == "unspecified" && defaultDriver != "" {
					if _, isDefExternal := drivers[defaultDriver]; isDefExternal {
						return fmt.Errorf(
							"external merge driver %q is not supported in read-only merge-check; only built-in Git merge drivers are evaluated",
							defaultDriver,
						)
					}
				}
			}
			return nil
		}
		if err := inspectAttributesAcrossSources(ctx, probeCtx, emptyHooksDir, "merge", touchedStdin, callerAttrSource, sourceTrees, checkDriverTokens); err != nil {
			return err
		}
	}

	if len(filters) > 0 {
		checkFilterTokens := func(tokens []string) error {
			for i := 0; i+2 < len(tokens); i += 3 {
				rawInfo := tokens[i+2]
				if _, isExternal := filters[rawInfo]; isExternal {
					return fmt.Errorf(
						"external conversion filter %q is not supported in read-only merge-check when renormalization is enabled; only built-in Git conversions are evaluated",
						rawInfo,
					)
				}
			}
			return nil
		}
		if err := inspectAttributesAcrossSources(ctx, probeCtx, emptyHooksDir, "filter", touchedStdin, callerAttrSource, sourceTrees, checkFilterTokens); err != nil {
			return err
		}
	}
	return nil
}

func rejectExternalCommitDiffFilters(ctx context.Context, workDir, emptyHooksDir, baseOID, headOID string, threeDot bool) error {
	if val, ok := getLastGitConfigValue(ctx, workDir, emptyHooksDir, "diff.renormalize", true); !ok || val != "true" {
		return nil
	}
	filters, _, err := listConfiguredExternalFilters(ctx, workDir, emptyHooksDir, true)
	if err != nil || len(filters) == 0 {
		return err
	}

	var touchedStdin []byte
	sourceTrees := []string{baseOID, headOID}
	if threeDot {
		var mergeBases []string
		touchedStdin, mergeBases, err = collectMergeTouchedPathsZ(ctx, workDir, emptyHooksDir, baseOID, headOID)
		sourceTrees = append(sourceTrees, mergeBases...)
	} else {
		dtOut, _, dtErr := runGitAlias(ctx, workDir, emptyHooksDir,
			fmt.Sprintf("diff-tree -r --name-only -z --no-commit-id %s %s", baseOID, headOID), nil)
		if dtErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("listing diff paths: %w", dtErr)
		}
		touchedStdin = dtOut
	}
	if err != nil || len(touchedStdin) == 0 {
		return err
	}

	probeCtx, err := resolveProbeRepoContext(ctx, workDir, emptyHooksDir, false)
	if err != nil {
		return err
	}
	callerAttrSource := hasEffectiveCallerAttrSource(ctx)
	return inspectAttributesAcrossSources(ctx, probeCtx, emptyHooksDir, "filter", touchedStdin, callerAttrSource, sourceTrees, func(tokens []string) error {
		for i := 0; i+2 < len(tokens); i += 3 {
			if _, isExternal := filters[tokens[i+2]]; isExternal {
				return fmt.Errorf(
					"external conversion filter %q is not supported in read-only diff; only built-in Git conversions are evaluated",
					tokens[i+2],
				)
			}
		}
		return nil
	})
}

func rejectExternalWorkingTreeDiffFilters(ctx context.Context, workDir, emptyHooksDir, compareTreeOID string) error {
	probeCtx, err := resolveProbeRepoContext(ctx, workDir, emptyHooksDir, true)
	if err != nil {
		return err
	}
	if !probeCtx.hasWorkTree {
		return nil
	}

	filters, allFilterNames, err := listConfiguredExternalFilters(ctx, workDir, emptyHooksDir, false)
	if err != nil || len(filters) == 0 {
		return err
	}

	candidateStdin, err := collectWorkingTreeDiffCandidatePathsZ(ctx, probeCtx, emptyHooksDir, compareTreeOID, allFilterNames)
	if err != nil || len(candidateStdin) == 0 {
		return err
	}

	callerAttrSource := hasEffectiveCallerAttrSource(ctx)
	var treeSources []string
	if compareTreeOID != "" {
		treeSources = []string{compareTreeOID}
	}
	return inspectAttributesAcrossSources(ctx, probeCtx, emptyHooksDir, "filter", candidateStdin, callerAttrSource, treeSources, func(tokens []string) error {
		for i := 0; i+2 < len(tokens); i += 3 {
			if _, isExternal := filters[tokens[i+2]]; isExternal {
				return fmt.Errorf(
					"external conversion filter %q is not supported in read-only diff; only built-in Git conversions are evaluated",
					tokens[i+2],
				)
			}
		}
		return nil
	})
}

type probeRepoContext struct {
	workDir       string
	attrDir       string
	absGitDir     string
	absCommonDir  string
	absObjectsDir string
	absIndexFile  string
	hasWorkTree   bool
}

func resolveProbeRepoContext(ctx context.Context, workDir, emptyHooksDir string, optionalNonRepo bool) (probeRepoContext, error) {
	baseEnv := readOnlyGitEnv(emptyHooksDir, nil)
	metaCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
		"rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir", "--git-path", "objects", "--git-path", "index")
	metaOut, _, metaErr := runPreparedGit(metaCmd, workDir, baseEnv)
	if metaErr != nil {
		if ctx.Err() != nil {
			return probeRepoContext{}, ctx.Err()
		}
		if optionalNonRepo {
			return probeRepoContext{workDir: workDir, attrDir: workDir}, nil
		}
		return probeRepoContext{}, fmt.Errorf("resolving repository metadata: %w", metaErr)
	}

	lines := strings.Split(strings.TrimRight(string(metaOut), "\r\n"), "\n")
	if len(lines) < 4 {
		return probeRepoContext{}, fmt.Errorf("unexpected git rev-parse output for %s", workDir)
	}
	info := probeRepoContext{
		workDir:       workDir,
		attrDir:       workDir,
		absGitDir:     strings.TrimRight(lines[0], "\r"),
		absCommonDir:  strings.TrimRight(lines[1], "\r"),
		absObjectsDir: strings.TrimRight(lines[2], "\r"),
		absIndexFile:  strings.TrimRight(lines[3], "\r"),
	}

	topCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "rev-parse", "--show-toplevel")
	if topOut, _, topErr := runPreparedGit(topCmd, workDir, baseEnv); topErr == nil {
		if top := strings.TrimRight(string(topOut), "\r\n"); top != "" {
			info.attrDir = top
			info.hasWorkTree = true
		}
	}
	return info, nil
}

func (p probeRepoContext) buildAttrDirEnv(base []string, emptyHooksDir string, extra map[string]string) []string {
	overrides := map[string]string{
		internalGitHooksDirEnvVar: emptyHooksDir,
	}
	if p.absGitDir != "" {
		overrides["GIT_DIR"] = p.absGitDir
	}
	if p.absCommonDir != "" {
		overrides["GIT_COMMON_DIR"] = p.absCommonDir
	}
	if p.absObjectsDir != "" {
		overrides["GIT_OBJECT_DIRECTORY"] = p.absObjectsDir
	}
	if p.absIndexFile != "" {
		overrides["GIT_INDEX_FILE"] = p.absIndexFile
	}
	if p.hasWorkTree && p.attrDir != "" {
		overrides["GIT_WORK_TREE"] = p.attrDir
	}
	for k, v := range extra {
		overrides[k] = v
	}
	return applyEnvOverrides(base, overrides)
}

func collectWorkingTreeDiffCandidatePathsZ(
	ctx context.Context,
	probeCtx probeRepoContext,
	emptyHooksDir, compareTreeOID string,
	allFilterNames []string,
) ([]byte, error) {
	seen := make(map[string]struct{})
	addPaths := func(raw []byte) {
		for _, p := range splitNulTokens(raw) {
			if p != "" {
				seen[p] = struct{}{}
			}
		}
	}

	existingCount, err := parseInheritedGitConfigCount()
	if err != nil {
		return nil, err
	}

	// Preserve all inherited GIT_CONFIG_PARAMETERS and GIT_CONFIG_KEY_0..{existingCount-1}
	// entries and append neutralizations at both GIT_CONFIG_COUNT and GIT_CONFIG_PARAMETERS
	// precedence so neither active nor caller-disabled filters can execute while rehashing
	// racy entries, even when GIT_CONFIG_PARAMETERS defines a nonempty filter command.
	extra := map[string]string{
		"GIT_CONFIG_COUNT": strconv.Itoa(existingCount + len(allFilterNames)*4),
	}
	idx := existingCount
	var paramPairs [][2]string
	for _, name := range allFilterNames {
		pairs := [][2]string{
			{"filter." + name + ".clean", ""},
			{"filter." + name + ".smudge", ""},
			{"filter." + name + ".process", ""},
			{"filter." + name + ".required", "false"},
		}
		for _, kv := range pairs {
			extra["GIT_CONFIG_KEY_"+strconv.Itoa(idx)] = kv[0]
			extra["GIT_CONFIG_VALUE_"+strconv.Itoa(idx)] = kv[1]
			idx++
		}
		paramPairs = append(paramPairs, pairs...)
	}
	if inheritedParams := getInheritedGitConfigParameters(); inheritedParams != "" && len(paramPairs) > 0 {
		extra["GIT_CONFIG_PARAMETERS"] = appendGitConfigParameters(inheritedParams, paramPairs)
	}
	neutralEnv := probeCtx.buildAttrDirEnv(os.Environ(), emptyHooksDir, extra)

	dfCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
		"diff-files", "--ignore-submodules=dirty", "--name-only", "-z", "--")
	dfOut, _, dfErr := runPreparedGit(dfCmd, probeCtx.attrDir, neutralEnv)
	if dfErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("listing modified working-tree paths: %w", dfErr)
	}
	addPaths(dfOut)

	if compareTreeOID != "" {
		diEnv := applyEnvOverrides(neutralEnv, map[string]string{
			internalGitAliasEnvVar: fmt.Sprintf("diff-index --ignore-submodules=dirty --name-only -z %s --", compareTreeOID),
		})
		diCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
			"--config-env=alias."+internalGitAliasName+"="+internalGitAliasEnvVar,
			internalGitAliasName)
		diOut, _, diErr := runPreparedGit(diCmd, probeCtx.attrDir, diEnv)
		if diErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("listing staged diff paths: %w", diErr)
		}
		addPaths(diOut)
	}

	idxInfo, statErr := os.Stat(probeCtx.absIndexFile)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			return sortedPathsToNulBytes(seen), nil
		}
		return nil, fmt.Errorf("statting git index: %w", statErr)
	}

	// Include racy-clean tracked paths (mtime >= index mtime truncated to seconds) because
	// git diff re-runs clean filters on racy-clean entries even when raw bytes match the index.
	racyFloor := idxInfo.ModTime().Truncate(time.Second)
	lsCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "ls-files", "-z")
	lsOut, _, lsErr := runPreparedGit(lsCmd, probeCtx.attrDir, neutralEnv)
	if lsErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("listing tracked index paths: %w", lsErr)
	}
	for _, relPath := range splitNulTokens(lsOut) {
		if relPath == "" {
			continue
		}
		if _, already := seen[relPath]; already {
			continue
		}
		fi, lstatErr := os.Lstat(filepath.Join(probeCtx.attrDir, filepath.FromSlash(relPath)))
		if lstatErr != nil || !fi.ModTime().Before(racyFloor) {
			seen[relPath] = struct{}{}
		}
	}

	return sortedPathsToNulBytes(seen), nil
}

func parseInheritedGitConfigCount() (int, error) {
	var raw string
	found := false
	for _, entry := range os.Environ() {
		name, val, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "GIT_CONFIG_COUNT") {
			raw = val
			found = true
		}
	}
	if !found || raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid GIT_CONFIG_COUNT %q", raw)
	}
	return n, nil
}

func getInheritedGitConfigParameters() string {
	if val := os.Getenv("GIT_CONFIG_PARAMETERS"); val != "" {
		return val
	}
	for _, entry := range os.Environ() {
		name, val, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, "GIT_CONFIG_PARAMETERS") && val != "" {
			return val
		}
	}
	return ""
}

func sqQuoteGitConfigToken(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func appendGitConfigParameters(existing string, pairs [][2]string) string {
	var b strings.Builder
	b.WriteString(existing)
	for _, kv := range pairs {
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(sqQuoteGitConfigToken(kv[0]))
		b.WriteByte('=')
		b.WriteString(sqQuoteGitConfigToken(kv[1]))
	}
	return b.String()
}

// inspectAttributesAcrossSources evaluates attrName ("merge" or "filter") across the
// active worktree/index stack and, when GIT_ATTR_SOURCE is not effectively supported/set,
// across each treeOID in sourceTreeOIDs using Git-2.38-compatible private temporary indexes:
// cached-only full tree, combined worktree + full-tree fallback, and (when root .gitattributes
// exists in treeOID) cached-only and combined worktree + root-only attribute index stacks.
func inspectAttributesAcrossSources(
	ctx context.Context,
	probeCtx probeRepoContext,
	emptyHooksDir, attrName string,
	pathsStdin []byte,
	callerAttrSource bool,
	sourceTreeOIDs []string,
	visit func(tokens []string) error,
) error {
	defaultEnv := probeCtx.buildAttrDirEnv(os.Environ(), emptyHooksDir, nil)
	defaultTokens, defaultErr := runCheckAttrProbe(ctx, probeCtx.attrDir, attrName, false, defaultEnv, pathsStdin)
	if defaultErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if callerAttrSource || len(sourceTreeOIDs) == 0 {
			return fmt.Errorf("checking %s attributes: %w", attrName, defaultErr)
		}
	} else if err := visit(defaultTokens); err != nil {
		return err
	}

	if callerAttrSource {
		return nil
	}

	probeIndex := filepath.Join(emptyHooksDir, "attr-probe.index")
	rootProbeIndex := filepath.Join(emptyHooksDir, "attr-root-probe.index")
	defer func() {
		_ = os.Remove(probeIndex)
		_ = os.Remove(rootProbeIndex)
	}()
	baseWithoutAttrSource := withoutEnvKeysCaseInsensitive(os.Environ(), "GIT_ATTR_SOURCE")
	seenTrees := make(map[string]struct{}, len(sourceTreeOIDs))
	for _, treeOID := range sourceTreeOIDs {
		if _, dup := seenTrees[treeOID]; dup || treeOID == "" {
			continue
		}
		seenTrees[treeOID] = struct{}{}
		_ = os.Remove(probeIndex)

		rtEnv := probeCtx.buildAttrDirEnv(baseWithoutAttrSource, emptyHooksDir, map[string]string{
			"GIT_INDEX_FILE":       probeIndex,
			internalGitAliasEnvVar: "read-tree " + treeOID,
		})
		rtCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
			"--config-env=alias."+internalGitAliasName+"="+internalGitAliasEnvVar,
			internalGitAliasName)
		if _, _, rtErr := runPreparedGit(rtCmd, probeCtx.attrDir, rtEnv); rtErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("reading tree %s into attribute probe index: %w", treeOID, rtErr)
		}

		indexEnv := probeCtx.buildAttrDirEnv(baseWithoutAttrSource, emptyHooksDir, map[string]string{
			"GIT_INDEX_FILE": probeIndex,
		})
		if err := probeIndexAttributeStacks(ctx, probeCtx, attrName, treeOID, indexEnv, pathsStdin, visit); err != nil {
			return err
		}

		// Populate a sparse root-only .gitattributes index matching merge-ort renormalization's
		// private root-attribute index, then probe both cached-only and worktree + root-index fallback.
		lsEnv := probeCtx.buildAttrDirEnv(baseWithoutAttrSource, emptyHooksDir, map[string]string{
			internalGitAliasEnvVar: "ls-tree -z " + treeOID + " -- .gitattributes",
		})
		lsCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
			"--config-env=alias."+internalGitAliasName+"="+internalGitAliasEnvVar,
			internalGitAliasName)
		lsOut, _, lsErr := runPreparedGit(lsCmd, probeCtx.attrDir, lsEnv)
		if lsErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("inspecting root .gitattributes in %s: %w", treeOID, lsErr)
		}
		if bytes.HasPrefix(lsOut, []byte("100644 blob ")) || bytes.HasPrefix(lsOut, []byte("100755 blob ")) {
			_ = os.Remove(rootProbeIndex)
			rootEnv := probeCtx.buildAttrDirEnv(baseWithoutAttrSource, emptyHooksDir, map[string]string{
				"GIT_INDEX_FILE": rootProbeIndex,
			})
			uiCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
				"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
				"update-index", "-z", "--index-info")
			uiCmd.Stdin = bytes.NewReader(lsOut)
			if _, _, uiErr := runPreparedGit(uiCmd, probeCtx.attrDir, rootEnv); uiErr != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("populating root attribute probe index for %s: %w", treeOID, uiErr)
			}
			if err := probeIndexAttributeStacks(ctx, probeCtx, attrName, treeOID, rootEnv, pathsStdin, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

func probeIndexAttributeStacks(
	ctx context.Context,
	probeCtx probeRepoContext,
	attrName, treeOID string,
	indexEnv []string,
	pathsStdin []byte,
	visit func(tokens []string) error,
) error {
	cachedTokens, cachedErr := runCheckAttrProbe(ctx, probeCtx.attrDir, attrName, true, indexEnv, pathsStdin)
	if cachedErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("checking cached %s attributes for %s: %w", attrName, treeOID, cachedErr)
	}
	if err := visit(cachedTokens); err != nil {
		return err
	}
	if probeCtx.hasWorkTree {
		combinedTokens, combinedErr := runCheckAttrProbe(ctx, probeCtx.attrDir, attrName, false, indexEnv, pathsStdin)
		if combinedErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("checking combined %s attributes for %s: %w", attrName, treeOID, combinedErr)
		}
		if err := visit(combinedTokens); err != nil {
			return err
		}
	}
	return nil
}

func runCheckAttrProbe(
	ctx context.Context,
	attrDir, attrName string,
	cached bool,
	env []string,
	pathsStdin []byte,
) ([]string, error) {
	var cmd *exec.Cmd
	switch {
	case attrName == "merge" && cached:
		cmd = exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "check-attr", "--cached", "-z", "--stdin", "merge")
	case attrName == "merge" && !cached:
		cmd = exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "check-attr", "-z", "--stdin", "merge")
	case cached:
		cmd = exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "check-attr", "--cached", "-z", "--stdin", "filter")
	default:
		cmd = exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "check-attr", "-z", "--stdin", "filter")
	}
	cmd.Stdin = bytes.NewReader(pathsStdin)
	out, _, err := runPreparedGit(cmd, attrDir, env)
	if err != nil {
		return nil, err
	}
	return splitNulTokens(out), nil
}

func hasEffectiveCallerAttrSource(ctx context.Context) bool {
	if !hasEnvVarCaseInsensitive("GIT_ATTR_SOURCE") {
		return false
	}
	rawVer, err := gitVersionOutputFunc(ctx)
	if err != nil {
		return false
	}
	majVer, minVer, _, ok := parseGitVersion(rawVer)
	if !ok {
		return false
	}
	return majVer > minAttrSourceGitMajor || (majVer == minAttrSourceGitMajor && minVer >= minAttrSourceGitMinor)
}

func listConfiguredExternalMergeDrivers(ctx context.Context, workDir, emptyHooksDir string) (map[string]struct{}, error) {
	env := withoutEnvKeysCaseInsensitive(readOnlyGitEnv(emptyHooksDir, nil), "GIT_CONFIG")
	cfgCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
		"config", "--includes", "--null", "--name-only", "--get-regexp", `^merge\..*\.driver$`)
	cfgOut, _, cfgErr := runPreparedGit(cfgCmd, workDir, env)
	if cfgErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(cfgErr, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("reading git merge driver configuration: %w", cfgErr)
	}
	drivers := make(map[string]struct{})
	for _, key := range splitNulTokens(cfgOut) {
		if name, ok := strings.CutPrefix(key, "merge."); ok {
			if name, ok = strings.CutSuffix(name, ".driver"); ok && name != "" {
				drivers[name] = struct{}{}
			}
		}
	}
	return drivers, nil
}

func listConfiguredExternalFilters(ctx context.Context, workDir, emptyHooksDir string, includeSmudge bool) (map[string]struct{}, []string, error) {
	env := withoutEnvKeysCaseInsensitive(readOnlyGitEnv(emptyHooksDir, nil), "GIT_CONFIG")
	cfgCmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
		"config", "--includes", "--null", "--get-regexp", `^filter\..*\.(clean|smudge|process)$`)
	cfgOut, _, cfgErr := runPreparedGit(cfgCmd, workDir, env)
	if cfgErr != nil {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(cfgErr, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil, nil
		}
		return nil, nil, fmt.Errorf("reading git filter configuration: %w", cfgErr)
	}

	type filterState struct {
		clean   string
		smudge  string
		process string
	}
	states := make(map[string]*filterState)
	for _, rec := range splitNulTokens(cfgOut) {
		key, val, hasNewline := strings.Cut(rec, "\n")
		if !hasNewline {
			val = "<nonbool>"
		}
		rest, ok := strings.CutPrefix(key, "filter.")
		if !ok {
			continue
		}
		dot := strings.LastIndexByte(rest, '.')
		if dot <= 0 {
			continue
		}
		name, prop := rest[:dot], rest[dot+1:]
		st := states[name]
		if st == nil {
			st = &filterState{}
			states[name] = st
		}
		switch prop {
		case "clean":
			st.clean = val
		case "smudge":
			st.smudge = val
		case "process":
			st.process = val
		}
	}

	active := make(map[string]struct{})
	allNames := make([]string, 0, len(states))
	for name, st := range states {
		allNames = append(allNames, name)
		if st.clean != "" || st.process != "" || (includeSmudge && st.smudge != "") {
			active[name] = struct{}{}
		}
	}
	slices.Sort(allNames)
	return active, allNames, nil
}

func isMergeRenormalizeEnabled(ctx context.Context, workDir, emptyHooksDir string) bool {
	if val, ok := getLastGitConfigValue(ctx, workDir, emptyHooksDir, "merge.renormalize", true); ok {
		return val == "true"
	}
	if val, ok := getLastGitConfigValue(ctx, workDir, emptyHooksDir, "diff.renormalize", true); ok {
		return val == "true"
	}
	return false
}

func getLastGitConfigValue(ctx context.Context, workDir, emptyHooksDir, key string, asBool bool) (string, bool) {
	env := withoutEnvKeysCaseInsensitive(readOnlyGitEnv(emptyHooksDir, nil), "GIT_CONFIG")
	var cmd *exec.Cmd
	switch {
	case key == "merge.default" && !asBool:
		cmd = exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
			"config", "--includes", "--null", "--get-all", "merge.default")
	case key == "merge.renormalize" && asBool:
		cmd = exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
			"config", "--includes", "--null", "--type=bool", "--get-all", "merge.renormalize")
	case key == "diff.renormalize" && asBool:
		cmd = exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
			"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
			"config", "--includes", "--null", "--type=bool", "--get-all", "diff.renormalize")
	default:
		return "", false
	}
	out, _, err := runPreparedGit(cmd, workDir, env)
	if err != nil || len(out) == 0 {
		return "", false
	}
	parts := bytes.Split(bytes.TrimSuffix(out, []byte{0}), []byte{0})
	raw := string(parts[len(parts)-1])
	if asBool {
		return strings.TrimSpace(raw), true
	}
	return raw, true
}

func withoutEnvKeysCaseInsensitive(base []string, keys ...string) []string {
	out := make([]string, 0, len(base))
	for _, entry := range base {
		name, _, ok := strings.Cut(entry, "=")
		if ok {
			skip := false
			for _, key := range keys {
				if strings.EqualFold(name, key) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
		}
		out = append(out, entry)
	}
	return out
}

func sortedPathsToNulBytes(seen map[string]struct{}) []byte {
	if len(seen) == 0 {
		return nil
	}
	sorted := make([]string, 0, len(seen))
	for p := range seen {
		sorted = append(sorted, p)
	}
	slices.Sort(sorted)
	var buf bytes.Buffer
	for _, p := range sorted {
		buf.WriteString(p)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}

func collectMergeTouchedPathsZ(ctx context.Context, workDir, emptyHooksDir, baseOID, headOID string) ([]byte, []string, error) {
	seen := make(map[string]struct{})
	addPaths := func(raw []byte) {
		for _, p := range splitNulTokens(raw) {
			if p != "" {
				seen[p] = struct{}{}
			}
		}
	}

	mbOut, _, mbErr := runGitAlias(ctx, workDir, emptyHooksDir, fmt.Sprintf("merge-base --all %s %s", baseOID, headOID), nil)
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}

	var bases []string
	if mbErr == nil {
		for _, field := range strings.Fields(string(mbOut)) {
			if isHexOID(field) {
				bases = append(bases, field)
			}
		}
	}

	if len(bases) > 0 {
		for _, mb := range bases {
			for _, side := range []string{baseOID, headOID} {
				dtOut, _, dtErr := runGitAlias(ctx, workDir, emptyHooksDir,
					fmt.Sprintf("diff-tree -r --name-only -z --no-commit-id %s %s", mb, side), nil)
				if dtErr != nil {
					if ctx.Err() != nil {
						return nil, nil, ctx.Err()
					}
					return nil, nil, fmt.Errorf("listing merge paths: %w", dtErr)
				}
				addPaths(dtOut)
			}
		}
	} else {
		for _, side := range []string{baseOID, headOID} {
			lsOut, _, lsErr := runGitAlias(ctx, workDir, emptyHooksDir,
				fmt.Sprintf("ls-tree --full-tree -r --name-only -z %s", side), nil)
			if lsErr != nil {
				if ctx.Err() != nil {
					return nil, nil, ctx.Err()
				}
				return nil, nil, fmt.Errorf("listing tree paths: %w", lsErr)
			}
			addPaths(lsOut)
		}
	}

	return sortedPathsToNulBytes(seen), bases, nil
}

func hasEnvVarCaseInsensitive(key string) bool {
	for _, entry := range os.Environ() {
		name, val, ok := strings.Cut(entry, "=")
		if ok && strings.EqualFold(name, key) && val != "" {
			return true
		}
	}
	return false
}

// resolveRevisionOID resolves a single Git revision to an object ID of wantType
// ("commit" or "tree"). It first resolves rev unmodified via git cat-file --batch-check
// on stdin so path/search expressions such as "HEAD:subdir" and ":/message" retain
// their exact meaning, then peels the resulting hex OID to wantType when needed.
func resolveRevisionOID(ctx context.Context, workDir, emptyHooksDir, rev, wantType string) (oid string, stderr []byte, err error) {
	firstOID, objType, errBytes, runErr := batchCheckObject(ctx, workDir, emptyHooksDir, rev)
	if runErr != nil {
		return "", errBytes, runErr
	}
	if objType == wantType {
		return firstOID, nil, nil
	}
	peeledOID, peeledType, peelErrBytes, peelErr := batchCheckObject(ctx, workDir, emptyHooksDir, firstOID+"^{"+wantType+"}")
	if peelErr != nil {
		return "", peelErrBytes, fmt.Errorf("revision %q does not resolve to a %s", rev, wantType)
	}
	if peeledType != wantType {
		return "", nil, fmt.Errorf("revision %q resolved to %s, expected %s", rev, peeledType, wantType)
	}
	return peeledOID, nil, nil
}

func batchCheckObject(ctx context.Context, workDir, emptyHooksDir, expr string) (oid, objType string, stderr []byte, err error) {
	env := readOnlyGitEnv(emptyHooksDir, nil)
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar, "cat-file", "--batch-check")
	cmd.Stdin = strings.NewReader(expr + "\n")
	out, errBytes, runErr := runPreparedGit(cmd, workDir, env)
	if runErr != nil {
		return "", "", errBytes, runErr
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) != 3 || !isHexOID(fields[0]) {
		return "", "", errBytes, fmt.Errorf("unknown revision %q", expr)
	}
	switch fields[1] {
	case "commit", "tree", "tag", "blob":
		return fields[0], fields[1], nil, nil
	default:
		return "", "", errBytes, fmt.Errorf("unknown revision %q", expr)
	}
}

func isHexOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func readOnlyGitEnv(emptyHooksDir string, extra map[string]string) []string {
	overrides := map[string]string{
		internalGitHooksDirEnvVar: emptyHooksDir,
	}
	for k, v := range extra {
		overrides[k] = v
	}
	return applyEnvOverrides(os.Environ(), overrides)
}

// runGitAlias invokes a non-shell Git alias at command-line config precedence via
// literal --config-env options and dedicated environment variables. Command-line
// precedence overrides inherited GIT_CONFIG_PARAMETERS, GIT_CONFIG_COUNT, worktree,
// and repository config while leaving GIT_DIR and ref backends untouched.
func runGitAlias(ctx context.Context, workDir, emptyHooksDir, aliasValue string, extraEnv map[string]string) (stdout, stderr []byte, err error) {
	overrides := map[string]string{
		internalGitAliasEnvVar: aliasValue,
	}
	for k, v := range extraEnv {
		overrides[k] = v
	}
	env := readOnlyGitEnv(emptyHooksDir, overrides)
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false",
		"--config-env=core.hooksPath="+internalGitHooksDirEnvVar,
		"--config-env=alias."+internalGitAliasName+"="+internalGitAliasEnvVar,
		internalGitAliasName)
	return runPreparedGit(cmd, workDir, env)
}

func runPreparedGit(cmd *exec.Cmd, workDir string, extraEnv []string) (stdout, stderr []byte, err error) {
	stopCancel := configureSubprocessCancel(cmd)
	defer stopCancel()
	if workDir != "" {
		cmd.Dir = workDir
	}
	if len(extraEnv) > 0 {
		cmd.Env = extraEnv
	}
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}
