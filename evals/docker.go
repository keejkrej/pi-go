// Ported from packages/evals/src/docker.ts (pi v1.0.0).

package evals

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/internal/xspawn"
)

// BuiltImage describes one built Docker image name and id for a documentation variant.
type BuiltImage struct {
	Name string `json:"name"`
	Id   string `json:"id"`
}

// BuiltImages holds the control and treatment Docker images built from evals/docker/Dockerfile.
type BuiltImages struct {
	WithoutDocs BuiltImage `json:"without_docs"`
	WithDocs    BuiltImage `json:"with_docs"`
}

// DockerCommandRunner runs an external docker invocation and reports exit status and optional captured stdout.
type DockerCommandRunner func(ctx context.Context, dir string, args []string, capture bool) (status int, stdout string, err error)

// DockerContext holds image identities, artifact output path, auth file path, and run parameters.
type DockerContext struct {
	Images            BuiltImages
	ArtifactDirectory string
	AuthPath          string
	Provider          string
	Model             string
	RunsPerVariant    int
	Runner            DockerCommandRunner
}

// DockerVariantImage returns the BuiltImage for variant.
func (b BuiltImages) DockerVariantImage(variant DocumentationVariant) BuiltImage {
	if variant == DocumentationVariantWithDocs {
		return b.WithDocs
	}
	return b.WithoutDocs
}

// DockerResolveRoots resolves (packageRoot, repositoryRoot) by locating go.mod upward from startDir (or cwd if empty).
func DockerResolveRoots(startDir ...string) (packageRoot string, repositoryRoot string, err error) {
	dir := ""
	if len(startDir) > 0 && startDir[0] != "" {
		dir = startDir[0]
	} else {
		dir, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	for {
		if info, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil && !info.IsDir() {
			return filepath.Join(dir, "evals"), dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", fmt.Errorf("Cannot locate repository root containing go.mod.")
		}
		dir = parent
	}
}

func dockerDefaultRunner(ctx context.Context, dir string, args []string, capture bool) (int, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := xspawn.CommandIn(ctx, dir, nil, "docker", args)
	var stdoutBuf bytes.Buffer
	if capture {
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = os.Stderr
	} else {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
	}
	err := cmd.Run()
	if err == nil {
		return 0, stdoutBuf.String(), nil
	}
	err = xspawn.VerifyENOENT(cmd, err)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), stdoutBuf.String(), nil
	}
	return 1, stdoutBuf.String(), err
}

func dockerExecute(ctx context.Context, runner DockerCommandRunner, dir string, args []string, capture bool) (int, string, error) {
	if runner == nil {
		runner = dockerDefaultRunner
	}
	return runner(ctx, dir, args, capture)
}

func dockerRequireSuccess(ctx context.Context, runner DockerCommandRunner, dir string, args []string) error {
	status, _, err := dockerExecute(ctx, runner, dir, args, false)
	if err != nil {
		return err
	}
	if status != 0 {
		return fmt.Errorf("docker exited with status %d.", status)
	}
	return nil
}

// BuildImages builds both without_docs and with_docs Docker images and inspects their IDs.
func BuildImages(ctx context.Context, runners ...DockerCommandRunner) (BuiltImages, error) {
	var runner DockerCommandRunner
	if len(runners) > 0 {
		runner = runners[0]
	}
	packageRoot, repositoryRoot, err := DockerResolveRoots()
	if err != nil {
		return BuiltImages{}, err
	}
	sum := sha256.Sum256([]byte(repositoryRoot))
	prefix := "pi-evals-" + hex.EncodeToString(sum[:])[:12]
	images := BuiltImages{
		WithoutDocs: BuiltImage{Name: prefix + "-without-docs:local"},
		WithDocs:    BuiltImage{Name: prefix + "-with-docs:local"},
	}

	buildVariant := func(variant DocumentationVariant, img *BuiltImage) error {
		if err := dockerRequireSuccess(ctx, runner, packageRoot, []string{
			"build",
			"--target",
			string(variant),
			"--tag",
			img.Name,
			"--file",
			filepath.Join(packageRoot, "docker", "Dockerfile"),
			repositoryRoot,
		}); err != nil {
			return err
		}
		status, stdout, err := dockerExecute(ctx, runner, packageRoot, []string{
			"image",
			"inspect",
			"--format",
			"{{.Id}}",
			img.Name,
		}, true)
		if err != nil {
			return err
		}
		id := strings.TrimSpace(stdout)
		if status != 0 || id == "" {
			return fmt.Errorf("Cannot inspect %s.", img.Name)
		}
		img.Id = id
		return nil
	}

	if err := buildVariant(DocumentationVariantWithoutDocs, &images.WithoutDocs); err != nil {
		return BuiltImages{}, err
	}
	if err := buildVariant(DocumentationVariantWithDocs, &images.WithDocs); err != nil {
		return BuiltImages{}, err
	}
	return images, nil
}

func dockerEnvFlag(name, value string) []string {
	return []string{"--env", name + "=" + value}
}

// RequireEvalAuthFile locates and validates auth.json for provider under PI_CODING_AGENT_DIR or ~/.pi/agent.
func RequireEvalAuthFile(provider string) (string, error) {
	agentDir := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR"))
	if agentDir != "" {
		abs, err := filepath.Abs(agentDir)
		if err != nil {
			return "", err
		}
		agentDir = abs
	} else {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		agentDir = filepath.Join(home, ".pi", "agent")
	}
	authPath := filepath.Join(agentDir, "auth.json")
	info, err := os.Stat(authPath)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		return "", fmt.Errorf("Eval authentication file does not exist: %s", authPath)
	}
	raw, err := os.ReadFile(authPath)
	if err != nil {
		return "", fmt.Errorf("Eval authentication file is invalid: %s: %w", authPath, err)
	}
	parsed, err := jsonx.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("Eval authentication file is invalid: %s: %w", authPath, err)
	}
	obj, ok := parsed.(*jsonx.Object)
	if !ok || obj == nil || !obj.Has(provider) {
		return "", fmt.Errorf("Eval authentication file has no credential for provider %s.", provider)
	}
	return authPath, nil
}

// DockerArgs builds container arguments and creates outputDirectory (0700) before execution.
func DockerArgs(
	dctx DockerContext,
	variant DocumentationVariant,
	outputDirectory string,
	entrypointArgs []string,
) ([]string, error) {
	if err := os.MkdirAll(outputDirectory, 0o700); err != nil {
		return nil, err
	}
	args := []string{
		"run",
		"--rm",
		"--read-only",
		"--tmpfs",
		"/tmp:rw,exec,mode=1777",
		"--mount",
		"type=bind,source=" + outputDirectory + ",target=/artifacts",
	}
	args = append(args, dockerEnvFlag("PI_EVAL_ARTIFACT_DIR", "/artifacts")...)
	args = append(args, dockerEnvFlag("PI_EVAL_RUNS_PER_VARIANT", strconv.Itoa(dctx.RunsPerVariant))...)
	args = append(args, dockerEnvFlag("PI_EVAL_SANDBOX_UID", "65532")...)
	args = append(args, dockerEnvFlag("PI_EVAL_SANDBOX_GID", "65532")...)
	args = append(args, dockerEnvFlag("PI_PROVIDER", dctx.Provider)...)
	args = append(args, dockerEnvFlag("PI_MODEL", dctx.Model)...)
	if uid, gid := os.Getuid(), os.Getgid(); uid >= 0 && gid >= 0 {
		args = append(args, dockerEnvFlag("PI_EVAL_ARTIFACT_UID", strconv.Itoa(uid))...)
		args = append(args, dockerEnvFlag("PI_EVAL_ARTIFACT_GID", strconv.Itoa(gid))...)
	}
	args = append(args, "--mount", "type=bind,source="+dctx.AuthPath+",target=/run/pi-eval-secrets/auth.json,readonly")
	args = append(args, dctx.Images.DockerVariantImage(variant).Name)
	args = append(args, entrypointArgs...)
	return args, nil
}

// CreateDockerContext constructs a DockerContext for running discovery and eval tasks.
func CreateDockerContext(
	images BuiltImages,
	artifactDirectory string,
	authPath string,
	provider string,
	model string,
	runsPerVariant int,
) DockerContext {
	return DockerContext{
		Images:            images,
		ArtifactDirectory: artifactDirectory,
		AuthPath:          authPath,
		Provider:          provider,
		Model:             model,
		RunsPerVariant:    runsPerVariant,
	}
}

// DiscoverCases runs case discovery inside the container image for variant and returns discovered-tests.json path.
func DiscoverCases(
	ctx context.Context,
	dctx DockerContext,
	variant DocumentationVariant,
	files []string,
	evalArgs []string,
) (string, error) {
	packageRoot, _, err := DockerResolveRoots()
	if err != nil {
		return "", err
	}
	outputDirectory := filepath.Join(dctx.ArtifactDirectory, "discovery", string(variant))
	entrypointArgs := make([]string, 0, 3+len(files)+len(evalArgs))
	entrypointArgs = append(entrypointArgs, "--discover", "--project", "docs")
	entrypointArgs = append(entrypointArgs, files...)
	entrypointArgs = append(entrypointArgs, evalArgs...)
	args, err := DockerArgs(dctx, variant, outputDirectory, entrypointArgs)
	if err != nil {
		return "", err
	}
	status, _, err := dockerExecute(ctx, dctx.Runner, packageRoot, args, false)
	if err != nil {
		return "", err
	}
	if status != 0 {
		return "", fmt.Errorf("%s eval discovery failed.", variant)
	}
	return filepath.Join(outputDirectory, "discovered-tests.json"), nil
}

func dockerTaskDirectoryName(task EvalTask) string {
	identity := jsonx.MustStringify([]any{task.EvalSet, task.CaseId, string(task.Variant), task.Model, task.RunNumber})
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

// RunTask executes one isolated eval task in Docker and returns the vitest.json report path if created.
func RunTask(ctx context.Context, dctx DockerContext, task EvalTask) (string, error) {
	packageRoot, _, err := DockerResolveRoots()
	if err != nil {
		return "", err
	}
	outputDirectory := filepath.Join(dctx.ArtifactDirectory, "tasks", dockerTaskDirectoryName(task))
	testName := task.EvalSet + " " + task.CaseId
	exactName := "^" + regexp.QuoteMeta(testName) + "$"
	args, err := DockerArgs(dctx, task.Variant, outputDirectory, []string{
		"--project",
		"docs",
		task.File,
		"--testNamePattern",
		exactName,
	})
	if err != nil {
		return "", err
	}
	if _, _, err := dockerExecute(ctx, dctx.Runner, packageRoot, args, false); err != nil {
		return "", err
	}
	reportPath := filepath.Join(outputDirectory, "vitest.json")
	info, statErr := os.Stat(reportPath)
	if statErr == nil && !info.IsDir() {
		return reportPath, nil
	}
	return "", nil
}
