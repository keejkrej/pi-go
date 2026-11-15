// Ported from packages/evals/docker/entrypoint.ts (pi v1.0.0).

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/evals/suites"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

const (
	mainDefaultPackageDir     = "/repo/package"
	mainDefaultSecretAuthPath = "/run/pi-eval-secrets/auth.json"
	mainDefaultAgentDir       = "/tmp/pi-eval-host-agent"
	mainDefaultBootstrapHome  = "/tmp/pi-eval-bootstrap"
	mainDefaultArtifactDir    = "/artifacts"
)

type mainConfig struct {
	Args          []string
	LookupEnv     func(string) (string, bool)
	SetEnv        func(string, string) error
	Suites        []*evals.EvalSuite
	PackageDir    string
	SecretAuth    string
	AgentDir      string
	BootstrapHome string
	ArtifactDir   string
	SkipChown     bool
	RunOptions    evals.SuiteRunOptions
	Stdout        io.Writer
	Stderr        io.Writer
}

type mainParsedArgs struct {
	Discover        bool
	Projects        []string
	Files           []string
	TestNamePattern string
}

type mainSelectedCase struct {
	Suite   *evals.EvalSuite
	Summary evals.EvalCaseSummary
}

func mainParseId(lookup func(string) (string, bool), name string) (int, error) {
	raw, ok := lookup(name)
	raw = strings.TrimSpace(raw)
	if !ok || raw == "" {
		return 0, fmt.Errorf("%s must be a positive integer.", name)
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v < 1 {
		return 0, fmt.Errorf("%s must be a positive integer.", name)
	}
	return v, nil
}

func mainValidatePackageDir(packageDir string, variant evals.DocumentationVariant) error {
	pkgJsonPath := filepath.Join(packageDir, "package.json")
	info, err := os.Stat(pkgJsonPath)
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("Installed coding-agent package is missing package.json: %s", pkgJsonPath)
	}
	for _, requiredDir := range []string{"theme", "export-html", "assets"} {
		dirPath := filepath.Join(packageDir, requiredDir)
		dInfo, dErr := os.Stat(dirPath)
		if dErr != nil || !dInfo.IsDir() {
			return fmt.Errorf("Installed coding-agent package is missing %s: %s", requiredDir, dirPath)
		}
	}

	switch variant {
	case evals.DocumentationVariantWithoutDocs:
		for _, forbidden := range []string{"README.md", "CHANGELOG.md", "docs", "examples"} {
			if _, statErr := os.Lstat(filepath.Join(packageDir, forbidden)); statErr == nil {
				return fmt.Errorf("without_docs contains coding-agent %s.", forbidden)
			}
		}
		return nil
	case evals.DocumentationVariantWithDocs:
		for _, rel := range []string{"README.md", "CHANGELOG.md", filepath.Join("docs", "models.md"), filepath.Join("examples", "README.md")} {
			full := filepath.Join(packageDir, rel)
			data, readErr := os.ReadFile(full)
			if readErr != nil || len(data) == 0 {
				return fmt.Errorf("Missing documentation: %s", full)
			}
		}
		return nil
	default:
		return fmt.Errorf("Eval image has no valid PI_EVAL_VARIANT.")
	}
}

func mainCopyAuthFile(src string, dstDir string) error {
	info, err := os.Stat(src)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return fmt.Errorf("Eval secret auth path is not a regular file: %s", src)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dstDir, "auth.json"), data, 0o600)
}

func mainParseArgs(args []string) (mainParsedArgs, error) {
	var parsed mainParsedArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--discover":
			parsed.Discover = true
		case arg == "--project" || arg == "-p":
			if i+1 >= len(args) {
				return mainParsedArgs{}, fmt.Errorf("Missing value for %s", arg)
			}
			i++
			proj := strings.TrimSpace(args[i])
			if proj != "" {
				parsed.Projects = append(parsed.Projects, proj)
			}
		case strings.HasPrefix(arg, "--project="):
			proj := strings.TrimSpace(strings.TrimPrefix(arg, "--project="))
			if proj != "" {
				parsed.Projects = append(parsed.Projects, proj)
			}
		case arg == "--testNamePattern" || arg == "-t":
			if i+1 >= len(args) {
				return mainParsedArgs{}, fmt.Errorf("Missing value for %s", arg)
			}
			i++
			parsed.TestNamePattern = args[i]
		case strings.HasPrefix(arg, "--testNamePattern="):
			parsed.TestNamePattern = strings.TrimPrefix(arg, "--testNamePattern=")
		case strings.HasPrefix(arg, "-"):
			return mainParsedArgs{}, fmt.Errorf("Unsupported entrypoint flag: %s", arg)
		default:
			if trimmed := strings.TrimSpace(arg); trimmed != "" {
				parsed.Files = append(parsed.Files, filepath.ToSlash(trimmed))
			}
		}
	}
	return parsed, nil
}

func mainMatchesFileFilter(suiteFile string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	normSuite := filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(suiteFile), "./"))
	baseSuite := filepath.Base(normSuite)
	for _, f := range filters {
		normFilter := filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(f), "./"))
		if normFilter == "" {
			continue
		}
		if normSuite == normFilter || baseSuite == normFilter || strings.HasSuffix(normSuite, "/"+normFilter) {
			return true
		}
	}
	return false
}

func mainFilterCases(allSuites []*evals.EvalSuite, parsed mainParsedArgs) ([]mainSelectedCase, error) {
	var namePattern *regexp.Regexp
	if parsed.TestNamePattern != "" {
		compiled, err := regexp.Compile(parsed.TestNamePattern)
		if err != nil {
			return nil, fmt.Errorf("Invalid --testNamePattern %q: %w", parsed.TestNamePattern, err)
		}
		namePattern = compiled
	}

	selected := make([]mainSelectedCase, 0)
	for _, suite := range allSuites {
		if suite == nil {
			continue
		}
		if len(parsed.Projects) > 0 && !slices.Contains(parsed.Projects, suite.Project) {
			continue
		}
		if !mainMatchesFileFilter(suite.File, parsed.Files) {
			continue
		}
		for _, summary := range suite.Describe() {
			vitestName := summary.EvalSet + " > " + summary.CaseId
			spaceName := summary.EvalSet + " " + summary.CaseId
			if namePattern != nil && !namePattern.MatchString(vitestName) && !namePattern.MatchString(spaceName) {
				continue
			}
			selected = append(selected, mainSelectedCase{
				Suite:   suite,
				Summary: summary,
			})
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("No eval cases matched the requested filters.")
	}
	return selected, nil
}

func mainWriteJsonFile(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := jsonx.MarshalIndent(value, "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o644)
}

func mainDiscover(artifactDir string, selected []mainSelectedCase) (string, error) {
	entries := make([]evals.DiscoveredTestEntry, 0, len(selected))
	for _, item := range selected {
		entries = append(entries, evals.DiscoveredTestEntry{
			Name: item.Summary.EvalSet + " > " + item.Summary.CaseId,
			File: item.Summary.File,
		})
	}
	outPath := filepath.Join(artifactDir, "discovered-tests.json")
	if err := mainWriteJsonFile(outPath, entries); err != nil {
		return "", err
	}
	return outPath, nil
}

func mainRunSuites(
	ctx context.Context,
	artifactDir string,
	selected []mainSelectedCase,
	runOpts evals.SuiteRunOptions,
	stdout io.Writer,
) (evals.VitestJsonReport, string, error) {
	startTime := time.Now().UnixMilli()
	byFile := make(map[string][]evals.VitestAssertionResult)
	fileOrder := make([]string, 0)
	passed := 0
	failed := 0
	total := 0

	for _, item := range selected {
		res, err := item.Suite.RunCase(ctx, item.Summary.CaseId, runOpts)
		if err != nil {
			res = evals.VitestAssertionResult{
				AncestorTitles:  []string{item.Summary.EvalSet},
				FullName:        item.Summary.EvalSet + " " + item.Summary.CaseId,
				Status:          "failed",
				Title:           item.Summary.CaseId,
				FailureMessages: []string{err.Error()},
			}
		}
		if _, exists := byFile[item.Summary.File]; !exists {
			fileOrder = append(fileOrder, item.Summary.File)
		}
		byFile[item.Summary.File] = append(byFile[item.Summary.File], res)
		total++
		if res.Status == "passed" {
			passed++
		} else {
			failed++
		}
		if stdout != nil {
			_, _ = fmt.Fprintf(stdout, "%s: %s (%s)\n", strings.ToUpper(res.Status), res.FullName, item.Summary.File)
			for _, msg := range res.FailureMessages {
				_, _ = fmt.Fprintf(stdout, "  %s\n", msg)
			}
		}
	}

	sort.Strings(fileOrder)
	testResults := make([]evals.VitestTestResult, 0, len(fileOrder))
	for _, file := range fileOrder {
		assertions := byFile[file]
		suiteStatus := "passed"
		var msg string
		for _, a := range assertions {
			if a.Status != "passed" {
				suiteStatus = "failed"
				if len(a.FailureMessages) > 0 {
					msg = a.FailureMessages[0]
				}
				break
			}
		}
		testResults = append(testResults, evals.VitestTestResult{
			Message:          msg,
			Name:             file,
			Status:           suiteStatus,
			AssertionResults: assertions,
		})
	}

	report := evals.VitestJsonReport{
		NumFailedTests: failed,
		NumPassedTests: passed,
		NumTotalTests:  total,
		StartTime:      startTime,
		Success:        failed == 0,
		TestResults:    testResults,
	}
	reportPath := filepath.Join(artifactDir, "vitest.json")
	if err := mainWriteJsonFile(reportPath, report); err != nil {
		return evals.VitestJsonReport{}, "", err
	}
	return report, reportPath, nil
}

func mainExecute(ctx context.Context, cfg mainConfig) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	lookup := cfg.LookupEnv
	if lookup == nil {
		lookup = os.LookupEnv
	}
	setEnv := cfg.SetEnv
	if setEnv == nil {
		setEnv = os.Setenv
	}

	sandboxUid, err := mainParseId(lookup, "PI_EVAL_SANDBOX_UID")
	if err != nil {
		return 1, err
	}
	sandboxGid, err := mainParseId(lookup, "PI_EVAL_SANDBOX_GID")
	if err != nil {
		return 1, err
	}

	rawVariant, _ := lookup("PI_EVAL_VARIANT")
	if strings.TrimSpace(rawVariant) == "" {
		return 1, fmt.Errorf(`PI_EVAL_VARIANT must be "without_docs" or "with_docs".`)
	}
	variant, err := evals.ResolveDocumentationVariant(rawVariant)
	if err != nil {
		return 1, err
	}

	packageDir := strings.TrimSpace(cfg.PackageDir)
	if packageDir == "" {
		if envDir, ok := lookup("PI_PACKAGE_DIR"); ok && strings.TrimSpace(envDir) != "" {
			packageDir = strings.TrimSpace(envDir)
		} else {
			packageDir = mainDefaultPackageDir
		}
	}
	if err := mainValidatePackageDir(packageDir, variant); err != nil {
		return 1, err
	}

	agentDir := strings.TrimSpace(cfg.AgentDir)
	if agentDir == "" {
		agentDir = mainDefaultAgentDir
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return 1, err
	}
	bootstrapHome := strings.TrimSpace(cfg.BootstrapHome)
	if bootstrapHome == "" {
		bootstrapHome = mainDefaultBootstrapHome
	}
	if err := os.MkdirAll(bootstrapHome, 0o755); err != nil {
		return 1, err
	}
	artifactDir := strings.TrimSpace(cfg.ArtifactDir)
	if artifactDir == "" {
		if envDir, ok := lookup("PI_EVAL_ARTIFACT_DIR"); ok && strings.TrimSpace(envDir) != "" {
			artifactDir = strings.TrimSpace(envDir)
		} else {
			artifactDir = mainDefaultArtifactDir
		}
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return 1, err
	}

	secretAuth := strings.TrimSpace(cfg.SecretAuth)
	if secretAuth == "" {
		secretAuth = mainDefaultSecretAuthPath
	}
	if err := mainCopyAuthFile(secretAuth, agentDir); err != nil {
		return 1, err
	}

	if !cfg.SkipChown {
		if err := evals.HarnessChownTree(agentDir, sandboxUid, sandboxGid); err != nil {
			return 1, err
		}
		if err := evals.HarnessChownTree(artifactDir, sandboxUid, sandboxGid); err != nil {
			return 1, err
		}
	}

	for _, kv := range [][2]string{
		{"HOME", bootstrapHome},
		{"USERPROFILE", bootstrapHome},
		{"PI_CODING_AGENT_DIR", agentDir},
		{"PI_EVAL_CONTAINER", "1"},
		{"PI_EVAL_ARTIFACT_DIR", artifactDir},
		{"PI_PACKAGE_DIR", packageDir},
		{"PI_EVAL_VARIANT", string(variant)},
	} {
		if err := setEnv(kv[0], kv[1]); err != nil {
			return 1, err
		}
	}

	parsed, err := mainParseArgs(cfg.Args)
	if err != nil {
		return 1, err
	}
	allSuites := cfg.Suites
	if len(allSuites) == 0 {
		allSuites = suites.AllSuites()
	}
	selected, err := mainFilterCases(allSuites, parsed)
	if err != nil {
		return 1, err
	}

	var exitCode int
	if parsed.Discover {
		if _, err := mainDiscover(artifactDir, selected); err != nil {
			return 1, err
		}
	} else {
		runOpts := cfg.RunOptions
		if runOpts.Variant == "" {
			runOpts.Variant = variant
		}
		report, _, err := mainRunSuites(ctx, artifactDir, selected, runOpts, cfg.Stdout)
		if err != nil {
			return 1, err
		}
		if !report.Success {
			exitCode = 1
		}
	}

	artifactUidRaw, hasArtifactUid := lookup("PI_EVAL_ARTIFACT_UID")
	artifactGidRaw, hasArtifactGid := lookup("PI_EVAL_ARTIFACT_GID")
	if !cfg.SkipChown && hasArtifactUid && hasArtifactGid && strings.TrimSpace(artifactUidRaw) != "" && strings.TrimSpace(artifactGidRaw) != "" {
		artifactUid, err := mainParseId(lookup, "PI_EVAL_ARTIFACT_UID")
		if err != nil {
			return 1, err
		}
		artifactGid, err := mainParseId(lookup, "PI_EVAL_ARTIFACT_GID")
		if err != nil {
			return 1, err
		}
		if err := evals.HarnessChownTree(artifactDir, artifactUid, artifactGid); err != nil {
			return 1, err
		}
	}
	return exitCode, nil
}

func main() {
	exitCode, err := mainExecute(context.Background(), mainConfig{
		Args:   os.Args[1:],
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "%v\n", err)
		utils.ConfigExit(1)
	}
	utils.ConfigExit(exitCode)
}
