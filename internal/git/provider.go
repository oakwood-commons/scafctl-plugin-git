// Package git implements a Git version control operations provider plugin for scafctl.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/go-logr/logr"
	"github.com/google/jsonschema-go/jsonschema"

	sdkplugin "github.com/oakwood-commons/scafctl-plugin-sdk/plugin"
	sdkprovider "github.com/oakwood-commons/scafctl-plugin-sdk/provider"
	"github.com/oakwood-commons/scafctl-plugin-sdk/provider/schemahelper"
)

// ProviderName is the name of this provider.
const ProviderName = "git"

// Field name constants for input/output map keys.
const (
	fieldOperation  = "operation"
	fieldRepository = "repository"
	fieldPath       = "path"
	fieldBranch     = "branch"
	fieldMessage    = "message"
	fieldFiles      = "files"
	fieldTag        = "tag"
	fieldRemote     = "remote"
	fieldDepth      = "depth"
	fieldUsername   = "username"
	fieldPassword   = "password"
	fieldForce      = "force"
)

// Plugin implements the sdkplugin.ProviderPlugin interface for git operations.
type Plugin struct {
	descriptor *sdkprovider.Descriptor
}

// NewPlugin creates a new git provider plugin instance.
func NewPlugin() *Plugin {
	return &Plugin{
		descriptor: buildDescriptor(),
	}
}

func buildDescriptor() *sdkprovider.Descriptor {
	version, _ := semver.NewVersion("1.0.0")
	maxOp := 50
	maxRepo := 1000
	maxPath := 500
	maxBranch := 200
	maxMsg := 1000
	maxItems := 100
	maxTag := 200
	maxRemote := 100
	maxDepth := 10000.0
	maxUser := 200
	maxPass := 500

	return &sdkprovider.Descriptor{
		Name:        ProviderName,
		DisplayName: "Git Provider",
		APIVersion:  "v1",
		Version:     version,
		Description: "Performs Git version control operations on local and remote repositories using the local git executable",
		Capabilities: []sdkprovider.Capability{
			sdkprovider.CapabilityAction,
			sdkprovider.CapabilityFrom,
		},
		SensitiveFields: []string{fieldPassword},
		Schema: schemahelper.ObjectSchema([]string{fieldOperation}, map[string]*jsonschema.Schema{
			fieldOperation: schemahelper.StringProp("Git operation to perform",
				schemahelper.WithExample("clone"),
				schemahelper.WithEnum("clone", "pull", "status", "add", "commit", "push", "checkout", "branch", "log", "tag"),
				schemahelper.WithMaxLength(maxOp)),
			fieldRepository: schemahelper.StringProp("Repository URL for clone operation",
				schemahelper.WithExample("https://github.com/user/repo.git"),
				schemahelper.WithMaxLength(maxRepo)),
			fieldPath: schemahelper.StringProp("Local path for repository",
				schemahelper.WithExample("/tmp/repo"),
				schemahelper.WithMaxLength(maxPath)),
			fieldBranch: schemahelper.StringProp("Branch name",
				schemahelper.WithExample("main"),
				schemahelper.WithMaxLength(maxBranch)),
			fieldMessage: schemahelper.StringProp("Commit message",
				schemahelper.WithExample("Update configuration"),
				schemahelper.WithMaxLength(maxMsg)),
			fieldFiles: schemahelper.ArrayProp("Files to add",
				schemahelper.WithMaxItems(maxItems)),
			fieldTag: schemahelper.StringProp("Tag name",
				schemahelper.WithExample("v1.0.0"),
				schemahelper.WithMaxLength(maxTag)),
			fieldRemote: schemahelper.StringProp("Remote name",
				schemahelper.WithExample("origin"),
				schemahelper.WithDefault("origin"),
				schemahelper.WithMaxLength(maxRemote)),
			fieldDepth: schemahelper.IntProp("Clone depth for shallow clone",
				schemahelper.WithExample("1"),
				schemahelper.WithMaximum(maxDepth)),
			fieldUsername: schemahelper.StringProp("Username for authentication",
				schemahelper.WithExample("user"),
				schemahelper.WithMaxLength(maxUser)),
			fieldPassword: schemahelper.StringProp("Password or token for authentication",
				schemahelper.WithExample("ghp_token"),
				schemahelper.WithWriteOnly(),
				schemahelper.WithMaxLength(maxPass)),
			fieldForce: schemahelper.BoolProp("Force the operation",
				schemahelper.WithExample("false"),
				schemahelper.WithDefault(false)),
		}),
		OutputSchemas: map[sdkprovider.Capability]*jsonschema.Schema{
			sdkprovider.CapabilityFrom: schemahelper.ObjectSchema(nil, map[string]*jsonschema.Schema{
				"output":       schemahelper.StringProp("Command output"),
				fieldOperation: schemahelper.StringProp("The operation that was performed"),
				fieldPath:      schemahelper.StringProp("Repository path used"),
			}),
			sdkprovider.CapabilityAction: schemahelper.ObjectSchema(nil, map[string]*jsonschema.Schema{
				"success":      schemahelper.BoolProp("Whether the operation succeeded"),
				"output":       schemahelper.StringProp("Command output"),
				"error":        schemahelper.StringProp("Error message if operation failed"),
				fieldOperation: schemahelper.StringProp("The operation that was performed"),
				fieldPath:      schemahelper.StringProp("Repository path used"),
			}),
		},
		Examples: []sdkprovider.Example{
			{
				Name:        "Clone repository",
				Description: "Clone a Git repository to a local path",
				YAML:        "name: clone-repo\nprovider: git\ninputs:\n  operation: clone\n  repository: \"https://github.com/user/repo.git\"\n  path: /tmp/repo",
			},
			{
				Name:        "Shallow clone",
				Description: "Clone only the latest commit for faster downloads",
				YAML:        "name: shallow-clone\nprovider: git\ninputs:\n  operation: clone\n  repository: \"https://github.com/user/repo.git\"\n  path: /tmp/repo\n  depth: 1",
			},
			{
				Name:        "Commit changes",
				Description: "Add files and commit changes to the repository",
				YAML:        "name: commit-changes\nprovider: git\ninputs:\n  operation: commit\n  path: /tmp/repo\n  message: \"Update configuration files\"\n  files:\n    - config.yaml\n    - settings.json",
			},
			{
				Name:        "Checkout branch",
				Description: "Switch to a different branch in the repository",
				YAML:        "name: checkout-feature\nprovider: git\ninputs:\n  operation: checkout\n  path: /tmp/repo\n  branch: feature-branch",
			},
			{
				Name:        "Push with authentication",
				Description: "Push changes to a remote repository with token authentication",
				YAML:        "name: push-changes\nprovider: git\ninputs:\n  operation: push\n  path: /tmp/repo\n  remote: origin\n  branch: main\n  username: user\n  password: ghp_secrettoken123",
			},
		},
		Tags: []string{"git", "vcs", "version-control", "filesystem"},
	}
}

// GetProviders returns the list of provider names this plugin provides.
func (p *Plugin) GetProviders(_ context.Context) ([]string, error) {
	return []string{ProviderName}, nil
}

// GetProviderDescriptor returns the descriptor for the named provider.
func (p *Plugin) GetProviderDescriptor(_ context.Context, name string) (*sdkprovider.Descriptor, error) {
	if name != ProviderName {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}
	return p.descriptor, nil
}

// ConfigureProvider configures the provider (no-op for git).
func (p *Plugin) ConfigureProvider(_ context.Context, name string, _ sdkplugin.ProviderConfig) error {
	if name != ProviderName {
		return fmt.Errorf("unknown provider: %s", name)
	}
	return nil
}

// ExecuteProvider performs the Git operation.
func (p *Plugin) ExecuteProvider(ctx context.Context, name string, inputs map[string]any) (*sdkprovider.Output, error) {
	if name != ProviderName {
		return nil, fmt.Errorf("unknown provider: %s", name)
	}

	lgr := logr.FromContextOrDiscard(ctx)

	operation, ok := inputs[fieldOperation].(string)
	if !ok || operation == "" {
		return nil, fmt.Errorf("%s: operation is required and must be a non-empty string", ProviderName)
	}

	lgr.V(1).Info("executing provider", "provider", ProviderName, "operation", operation)

	if dryRun := sdkprovider.DryRunFromContext(ctx); dryRun {
		result := executeDryRun(operation, inputs)
		lgr.V(1).Info("provider completed (dry-run)", "provider", ProviderName)
		return result, nil
	}

	result, err := executeGitOperation(ctx, operation, inputs)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ProviderName, err)
	}

	lgr.V(1).Info("provider completed", "provider", ProviderName)
	return result, nil
}

// ExecuteProviderStream is not supported for the git provider.
func (p *Plugin) ExecuteProviderStream(_ context.Context, _ string, _ map[string]any, _ func(sdkplugin.StreamChunk)) error {
	return sdkplugin.ErrStreamingNotSupported
}

// DescribeWhatIf returns a human-readable description of what the operation would do.
func (p *Plugin) DescribeWhatIf(_ context.Context, name string, inputs map[string]any) (string, error) {
	if name != ProviderName {
		return "", fmt.Errorf("unknown provider: %s", name)
	}

	operation, _ := inputs[fieldOperation].(string)
	path, _ := inputs[fieldPath].(string)

	switch operation {
	case "clone":
		repo, _ := inputs[fieldRepository].(string)
		msg := fmt.Sprintf("Would clone %s", repo)
		if path != "" {
			msg += fmt.Sprintf(" to %s", path)
		}
		if branch, ok := inputs[fieldBranch].(string); ok && branch != "" {
			msg += fmt.Sprintf(" (branch: %s)", branch)
		}
		return msg, nil
	case "commit":
		message, _ := inputs[fieldMessage].(string)
		return fmt.Sprintf("Would commit in %s with message: %s", path, message), nil
	case "push":
		branch, _ := inputs[fieldBranch].(string)
		return fmt.Sprintf("Would push %s to branch %q", path, branch), nil
	case "checkout":
		branch, _ := inputs[fieldBranch].(string)
		return fmt.Sprintf("Would checkout branch %q in %s", branch, path), nil
	case "tag":
		tag, _ := inputs[fieldTag].(string)
		return fmt.Sprintf("Would create tag %q in %s", tag, path), nil
	default:
		if path != "" {
			return fmt.Sprintf("Would perform git %s on %s", operation, path), nil
		}
		return fmt.Sprintf("Would perform git %s", operation), nil
	}
}

// ExtractDependencies returns nil (git operations have no extractable dependencies).
func (p *Plugin) ExtractDependencies(_ context.Context, _ string, _ map[string]any) ([]string, error) {
	return nil, nil
}

// StopProvider is a no-op for the git provider.
func (p *Plugin) StopProvider(_ context.Context, _ string) error {
	return nil
}

// =============================================================================
// Git operation handlers
// =============================================================================

func executeGitOperation(ctx context.Context, operation string, inputs map[string]any) (*sdkprovider.Output, error) {
	switch operation {
	case "clone":
		return executeClone(ctx, inputs)
	case "pull":
		return executePull(ctx, inputs)
	case "status":
		return executeStatus(ctx, inputs)
	case "add":
		return executeAdd(ctx, inputs)
	case "commit":
		return executeCommit(ctx, inputs)
	case "push":
		return executePush(ctx, inputs)
	case "checkout":
		return executeCheckout(ctx, inputs)
	case "branch":
		return executeBranch(ctx, inputs)
	case "log":
		return executeLog(ctx, inputs)
	case "tag":
		return executeTag(ctx, inputs)
	default:
		return nil, fmt.Errorf("unsupported operation: %s", operation)
	}
}

func executeClone(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	repository, ok := inputs[fieldRepository].(string)
	if !ok || repository == "" {
		return nil, fmt.Errorf("repository URL is required for clone operation")
	}

	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for clone operation")
	}

	args := []string{"clone"}

	if depthRaw, ok := inputs[fieldDepth]; ok {
		var depth int
		switch v := depthRaw.(type) {
		case int:
			depth = v
		case float64:
			depth = int(v)
		}
		if depth > 0 {
			args = append(args, "--depth", fmt.Sprint(depth))
		}
	}

	if branch, ok := inputs[fieldBranch].(string); ok && branch != "" {
		args = append(args, "--branch", branch)
	}

	repoURL := repository
	var credCleanup func()
	var credEnv []string
	if username, ok := inputs[fieldUsername].(string); ok && username != "" {
		if password, ok := inputs[fieldPassword].(string); ok && password != "" {
			var err error
			credEnv, credCleanup, err = createNetrcCredentials(repository, username, password)
			if err != nil {
				return nil, fmt.Errorf("setting up git credentials: %w", err)
			}
		}
	}
	if credCleanup != nil {
		defer credCleanup()
	}

	args = append(args, repoURL, path)

	return runGitCommand(ctx, "", args, "clone", credEnv)
}

func executePull(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for pull operation")
	}

	remote, _ := inputs[fieldRemote].(string)
	if remote == "" {
		remote = "origin"
	}

	args := []string{"pull", remote}

	if branch, ok := inputs[fieldBranch].(string); ok && branch != "" {
		args = append(args, branch)
	}

	return runGitCommand(ctx, path, args, "pull", nil)
}

func executeStatus(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for status operation")
	}

	args := []string{"status", "--porcelain"}

	return runGitCommand(ctx, path, args, "status", nil)
}

func executeAdd(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for add operation")
	}

	args := []string{"add"}

	filesRaw, ok := inputs[fieldFiles]
	if !ok || filesRaw == nil {
		return nil, fmt.Errorf("files is required for add operation")
	}

	switch v := filesRaw.(type) {
	case []any:
		for _, file := range v {
			args = append(args, fmt.Sprint(file))
		}
	case []string:
		args = append(args, v...)
	default:
		return nil, fmt.Errorf("files must be an array")
	}

	return runGitCommand(ctx, path, args, "add", nil)
}

func executeCommit(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for commit operation")
	}

	message, ok := inputs[fieldMessage].(string)
	if !ok || message == "" {
		return nil, fmt.Errorf("message is required for commit operation")
	}

	args := []string{"commit", "-m", message}

	return runGitCommand(ctx, path, args, "commit", nil)
}

func executePush(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for push operation")
	}

	remote, _ := inputs[fieldRemote].(string)
	if remote == "" {
		remote = "origin"
	}

	args := []string{"push", remote}

	if branch, ok := inputs[fieldBranch].(string); ok && branch != "" {
		args = append(args, branch)
	}

	if force, ok := inputs[fieldForce].(bool); ok && force {
		args = append(args, "--force")
	}

	return runGitCommand(ctx, path, args, "push", nil)
}

func executeCheckout(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for checkout operation")
	}

	branch, ok := inputs[fieldBranch].(string)
	if !ok || branch == "" {
		return nil, fmt.Errorf("branch is required for checkout operation")
	}

	args := []string{"checkout", branch}

	return runGitCommand(ctx, path, args, "checkout", nil)
}

func executeBranch(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for branch operation")
	}

	args := []string{"branch"}

	if branch, ok := inputs[fieldBranch].(string); ok && branch != "" {
		args = append(args, branch)
	} else {
		args = append(args, "-a")
	}

	return runGitCommand(ctx, path, args, "branch", nil)
}

func executeLog(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for log operation")
	}

	args := []string{"log", "--oneline", "-n", "10"}

	return runGitCommand(ctx, path, args, "log", nil)
}

func executeTag(ctx context.Context, inputs map[string]any) (*sdkprovider.Output, error) {
	path, _ := inputs[fieldPath].(string)
	if path == "" {
		return nil, fmt.Errorf("path is required for tag operation")
	}

	tag, ok := inputs[fieldTag].(string)
	if !ok || tag == "" {
		return runGitCommand(ctx, path, []string{"tag"}, "tag", nil)
	}

	args := []string{"tag", tag}

	if message, ok := inputs[fieldMessage].(string); ok && message != "" {
		args = append(args, "-m", message)
	}

	return runGitCommand(ctx, path, args, "tag", nil)
}

// =============================================================================
// Git command execution
// =============================================================================

func runGitCommand(ctx context.Context, workDir string, args []string, operation string, extraEnv []string) (*sdkprovider.Output, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // args are validated before reaching here

	if workDir != "" {
		if operation != "clone" {
			if _, err := os.Stat(workDir); os.IsNotExist(err) {
				return nil, fmt.Errorf("directory does not exist: %s", workDir)
			}
		}
		cmd.Dir = workDir
	}

	if len(extraEnv) > 0 {
		cmd.Env = extraEnv
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	success := true
	errorMsg := ""

	if err != nil {
		success = false
		exitErr := &exec.ExitError{}
		if errors.As(err, &exitErr) {
			errorMsg = stderr.String()
			if errorMsg == "" {
				errorMsg = fmt.Sprintf("git command failed with exit code %d", exitErr.ExitCode())
			}
		} else {
			return nil, fmt.Errorf("failed to execute git command: %w", err)
		}
	}

	output := stdout.String()
	if output == "" && stderr.String() != "" {
		output = stderr.String()
	}

	return &sdkprovider.Output{
		Data: map[string]any{
			"success":      success,
			"output":       strings.TrimSpace(output),
			"error":        errorMsg,
			fieldOperation: operation,
			fieldPath:      workDir,
		},
	}, nil
}

// =============================================================================
// Dry-run
// =============================================================================

func executeDryRun(operation string, inputs map[string]any) *sdkprovider.Output {
	message := fmt.Sprintf("Would execute git %s", operation)

	if repository, ok := inputs[fieldRepository].(string); ok && repository != "" {
		message += fmt.Sprintf(" on repository: %s", repository)
	}

	if path, ok := inputs[fieldPath].(string); ok && path != "" {
		message += fmt.Sprintf(" at path: %s", path)
	}

	if branch, ok := inputs[fieldBranch].(string); ok && branch != "" {
		message += fmt.Sprintf(" for branch: %s", branch)
	}

	return &sdkprovider.Output{
		Data: map[string]any{
			"success":      true,
			"output":       "",
			"error":        "",
			fieldOperation: operation,
			fieldPath:      inputs[fieldPath],
			"_dryRun":      true,
			"_message":     message,
		},
	}
}

// =============================================================================
// Credential management
// =============================================================================

// createNetrcCredentials creates a temporary .netrc file for HTTPS git authentication
// and returns a merged environment (based on os.Environ()) that sets HOME to the temp
// directory. Using a .netrc file avoids embedding credentials in process arguments
// where they would be visible via ps, /proc, or audit logs.
//
// The caller MUST invoke the returned cleanup function to remove the temp directory.
func createNetrcCredentials(repoURL, username, password string) (env []string, cleanup func(), err error) {
	if !strings.HasPrefix(repoURL, "http://") && !strings.HasPrefix(repoURL, "https://") {
		// Non-HTTP(S) scheme (e.g. SSH) -- credentials not applicable via netrc.
		return nil, func() {}, nil
	}

	u, parseErr := url.Parse(repoURL)
	if parseErr != nil {
		return nil, nil, fmt.Errorf("parsing repository URL: %w", parseErr)
	}

	host := u.Hostname()
	if host == "" {
		return nil, nil, fmt.Errorf("repository URL has no hostname")
	}

	// Validate that username and password do not contain whitespace or control characters.
	// The netrc format is whitespace-delimited, so embedded spaces/tabs/newlines
	// would corrupt the file and could inject additional machine entries.
	for _, field := range []struct{ name, val string }{{"username", username}, {"password", password}} {
		for _, r := range field.val {
			if r <= 0x20 || r == 0x7f {
				return nil, nil, fmt.Errorf("%s contains whitespace or control characters, which are not allowed in netrc credentials", field.name)
			}
		}
	}

	tmpDir, mkErr := os.MkdirTemp("", ".scafctl-git-creds-*")
	if mkErr != nil {
		return nil, nil, fmt.Errorf("creating credential temp dir: %w", mkErr)
	}

	cleanupFn := func() { os.RemoveAll(tmpDir) } //nolint:errcheck,gosec // G104: cleanup func, error intentionally discarded

	// Write .netrc (Unix) and _netrc (Windows / Git for Windows).
	// The file names are hardcoded literals -- no path traversal is possible.
	netrcContent := fmt.Sprintf("machine %s\nlogin %s\npassword %s\n", host, username, password)
	for _, name := range []string{".netrc", "_netrc"} {
		netrcPath := filepath.Join(tmpDir, name) //nolint:gosec // name is a hardcoded literal
		if writeErr := os.WriteFile(netrcPath, []byte(netrcContent), 0o600); writeErr != nil {
			cleanupFn()
			return nil, nil, fmt.Errorf("writing credential file: %w", writeErr)
		}
	}

	// Build env: start from the current process environment, override HOME / USERPROFILE
	// so git picks up the .netrc, and preserve the real global git config.
	overrides := map[string]string{
		"HOME":                tmpDir,
		"USERPROFILE":         tmpDir,
		"GIT_TERMINAL_PROMPT": "0",
	}
	if homeDir, hdErr := os.UserHomeDir(); hdErr == nil {
		globalConfig := filepath.Join(homeDir, ".gitconfig")
		if _, statErr := os.Stat(globalConfig); statErr == nil {
			overrides["GIT_CONFIG_GLOBAL"] = globalConfig
		} else {
			// Fallback: check XDG-style git config location used when ~/.gitconfig is absent.
			xdgConfigHome := os.Getenv("XDG_CONFIG_HOME")
			if xdgConfigHome == "" {
				xdgConfigHome = filepath.Join(homeDir, ".config")
			}
			xdgGitConfig := filepath.Clean(filepath.Join(xdgConfigHome, "git", "config"))
			if _, xdgErr := os.Stat(xdgGitConfig); xdgErr == nil { //nolint:gosec // xdgConfigHome is from the user's own environment
				overrides["GIT_CONFIG_GLOBAL"] = xdgGitConfig
			}
		}
	}

	mergedEnv := applyEnvOverrides(os.Environ(), overrides)
	return mergedEnv, cleanupFn, nil
}

// applyEnvOverrides returns a copy of base with the specified key=value pairs
// overriding any existing entries for those keys. Key comparison is
// case-insensitive so that Windows env vars (which are case-insensitive) are
// matched correctly.
func applyEnvOverrides(base []string, overrides map[string]string) []string {
	// Build an upper-cased lookup so key comparison is case-insensitive.
	upperOverrides := make(map[string]struct{}, len(overrides))
	for k := range overrides {
		upperOverrides[strings.ToUpper(k)] = struct{}{}
	}

	result := make([]string, 0, len(base)+len(overrides))
	for _, e := range base {
		key := e
		if idx := strings.Index(e, "="); idx >= 0 {
			key = e[:idx]
		}
		if _, overridden := upperOverrides[strings.ToUpper(key)]; !overridden {
			result = append(result, e)
		}
	}
	for k, v := range overrides {
		result = append(result, k+"="+v)
	}
	return result
}
