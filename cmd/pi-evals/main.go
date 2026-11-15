// Ported from packages/evals/src/cli.ts (pi v1.0.0).

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/keejkrej/pi-go/codingagent/utils"
	"github.com/keejkrej/pi-go/evals"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

type mainCliOptions struct {
	Provider       string
	Model          string
	RunsPerVariant int
	RequestedFiles []string
	DiscoveryArgs  []string
}

type mainProtocolCase struct {
	EvalSet string `json:"evalSet"`
	CaseId  string `json:"caseId"`
	File    string `json:"file"`
}

type mainProtocolPayload struct {
	SchemaVersion  int                `json:"schemaVersion"`
	Model          string             `json:"model"`
	RunsPerVariant int                `json:"runsPerVariant"`
	Images         evals.BuiltImages  `json:"images"`
	Files          []string           `json:"files"`
	Cases          []mainProtocolCase `json:"cases"`
	Tasks          []evals.EvalTask   `json:"tasks"`
}

type mainProtocolFile struct {
	mainProtocolPayload
	ProtocolDigest string `json:"protocolDigest"`
}

type mainDeps struct {
	stdout      io.Writer
	stderr      io.Writer
	env         map[string]string
	packageRoot string
	buildImages func(ctx context.Context) (evals.BuiltImages, error)
	requireAuth func(provider string) (string, error)
	discover    func(ctx context.Context, dctx evals.DockerContext, variant evals.DocumentationVariant, files []string, evalArgs []string) (string, error)
	runTask     func(ctx context.Context, dctx evals.DockerContext, task evals.EvalTask) (string, error)
}

func mainLookupEnv(env map[string]string, key string) string {
	if env != nil {
		return env[key]
	}
	return os.Getenv(key)
}

func mainIsEvalFileArg(arg string) bool {
	return strings.HasSuffix(arg, ".docs.eval.ts") ||
		strings.HasSuffix(arg, "_docs_eval.go") ||
		strings.HasSuffix(arg, ".eval.ts") ||
		strings.HasSuffix(arg, "_eval.go")
}

func mainIsDocEvalFile(file string) bool {
	return strings.HasSuffix(file, ".docs.eval.ts") || strings.HasSuffix(file, "_docs_eval.go")
}

func mainParseEvalCli(args []string, env map[string]string) (mainCliOptions, error) {
	var provider string
	var model string
	var runsText string
	hasRunsText := false
	cliSelectedModel := false
	var requestedFiles []string
	var discoveryArgs []string

	for i := 0; i < len(args); i++ {
		arg := args[i]
		if mainIsEvalFileArg(arg) {
			requestedFiles = append(requestedFiles, arg)
			continue
		}
		if arg == "-t" || arg == "--testNamePattern" {
			if i+1 >= len(args) || args[i+1] == "" {
				return mainCliOptions{}, fmt.Errorf("Missing value for %s.", arg)
			}
			discoveryArgs = append(discoveryArgs, arg, args[i+1])
			i++
			continue
		}
		if strings.HasPrefix(arg, "--testNamePattern=") {
			if len(arg) == len("--testNamePattern=") {
				return mainCliOptions{}, fmt.Errorf("Missing value for --testNamePattern.")
			}
			discoveryArgs = append(discoveryArgs, arg)
			continue
		}

		name := arg
		value := ""
		hasEquals := false
		if eqIdx := strings.IndexByte(arg, '='); eqIdx != -1 {
			name = arg[:eqIdx]
			value = arg[eqIdx+1:]
			hasEquals = true
		}
		if name == "--provider" || name == "--model" || name == "--runs-per-variant" {
			if !hasEquals {
				if i+1 >= len(args) {
					return mainCliOptions{}, fmt.Errorf("Missing value for %s.", name)
				}
				value = args[i+1]
				i++
			}
			if value == "" || (!hasEquals && strings.HasPrefix(value, "-")) {
				return mainCliOptions{}, fmt.Errorf("Missing value for %s.", name)
			}
			switch name {
			case "--provider":
				provider = value
				cliSelectedModel = true
			case "--model":
				model = value
				cliSelectedModel = true
			case "--runs-per-variant":
				runsText = value
				hasRunsText = true
			}
			continue
		}
		return mainCliOptions{}, fmt.Errorf("Unsupported eval argument: %s", arg)
	}

	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if cliSelectedModel {
		if provider == "" || model == "" {
			return mainCliOptions{}, fmt.Errorf("CLI model selection requires both --provider and --model.")
		}
	} else {
		provider = strings.TrimSpace(mainLookupEnv(env, "PI_PROVIDER"))
		model = strings.TrimSpace(mainLookupEnv(env, "PI_MODEL"))
		if (provider != "") != (model != "") {
			return mainCliOptions{}, fmt.Errorf("Set both PI_PROVIDER and PI_MODEL, or neither.")
		}
	}

	configuredRuns := ""
	if hasRunsText {
		configuredRuns = strings.TrimSpace(runsText)
	} else {
		configuredRuns = strings.TrimSpace(mainLookupEnv(env, "PI_EVAL_RUNS_PER_VARIANT"))
	}
	runsPerVariant := 1
	if configuredRuns != "" {
		parsed, err := strconv.Atoi(configuredRuns)
		if err != nil || parsed < 1 {
			return mainCliOptions{}, fmt.Errorf("Runs per variant must be a positive integer.")
		}
		runsPerVariant = parsed
	}

	return mainCliOptions{
		Provider:       provider,
		Model:          model,
		RunsPerVariant: runsPerVariant,
		RequestedFiles: requestedFiles,
		DiscoveryArgs:  discoveryArgs,
	}, nil
}

func mainArtifactRunId() string {
	ts := strings.ReplaceAll(time.Now().UTC().Format("2006-01-02T15-04-05.000Z"), ":", "-")
	return ts + "_" + uuid.NewString()
}

func mainContainerPath(packageRoot string, path string) (string, error) {
	resolved := path
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(packageRoot, path)
	}
	absRoot, err := filepath.Abs(packageRoot)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("Eval file must be inside %s: %s", absRoot, path)
	}
	return filepath.ToSlash(rel), nil
}

func mainNormalizeDiscoveredFile(path string) (string, error) {
	normalized := filepath.ToSlash(path)
	for _, prefix := range []string{"/repo/evals/", "/repo/packages/evals/"} {
		if strings.HasPrefix(normalized, prefix) {
			return strings.TrimPrefix(normalized, prefix), nil
		}
	}
	if strings.HasPrefix(normalized, "/") {
		return "", fmt.Errorf("Discovered eval path is outside the container package: %s", path)
	}
	if strings.HasPrefix(normalized, "evals/") {
		return strings.TrimPrefix(normalized, "evals/"), nil
	}
	return normalized, nil
}

func mainCompareDiscovery(left []evals.DiscoveredEvalCase, right []evals.DiscoveredEvalCase) error {
	encodeSet := func(cases []evals.DiscoveredEvalCase) ([]string, error) {
		out := make([]string, 0, len(cases))
		for _, c := range cases {
			normFile, err := mainNormalizeDiscoveredFile(c.File)
			if err != nil {
				return nil, err
			}
			out = append(out, jsonx.MustStringify([]string{c.FullName, normFile}))
		}
		slices.Sort(out)
		return out, nil
	}
	lEnc, err := encodeSet(left)
	if err != nil {
		return err
	}
	rEnc, err := encodeSet(right)
	if err != nil {
		return err
	}
	if jsonx.MustStringify(lEnc) != jsonx.MustStringify(rEnc) {
		return fmt.Errorf("Documentation variants discovered different eval cases.")
	}
	return nil
}

func mainDiscoverDefaultDocFiles(packageRoot string) ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(packageRoot, "suites", "*_docs_eval.go"))
	if err != nil {
		return nil, err
	}
	sortFiles := make([]string, 0, len(matches))
	for _, m := range matches {
		rel, err := mainContainerPath(packageRoot, m)
		if err != nil {
			return nil, err
		}
		sortFiles = append(sortFiles, rel)
	}
	slices.Sort(sortFiles)
	return sortFiles, nil
}

func mainRun(ctx context.Context, args []string, deps mainDeps) int {
	stdout := deps.stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := deps.stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	cli, err := mainParseEvalCli(args, deps.env)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if cli.Provider == "" || cli.Model == "" {
		fmt.Fprintln(stderr, "Set PI_PROVIDER and PI_MODEL, or pass --provider and --model.")
		return 1
	}
	modelIdentity := cli.Provider + "/" + cli.Model

	packageRoot := deps.packageRoot
	if packageRoot == "" {
		pkgRoot, _, err := evals.DockerResolveRoots()
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		packageRoot = pkgRoot
	}

	var files []string
	if len(cli.RequestedFiles) > 0 {
		for _, f := range cli.RequestedFiles {
			rel, err := mainContainerPath(packageRoot, f)
			if err != nil {
				fmt.Fprintln(stderr, err.Error())
				return 1
			}
			files = append(files, rel)
		}
		slices.Sort(files)
	} else {
		files, err = mainDiscoverDefaultDocFiles(packageRoot)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "No documentation eval files were selected.")
		return 1
	}
	for _, file := range files {
		if !mainIsDocEvalFile(file) {
			fmt.Fprintf(stderr, "Documentation runner cannot execute non-doc eval: %s\n", file)
			return 1
		}
	}

	requireAuth := deps.requireAuth
	if requireAuth == nil {
		requireAuth = evals.RequireEvalAuthFile
	}
	authPath, err := requireAuth(cli.Provider)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	artifactDirectory := filepath.Join(packageRoot, ".eval", mainArtifactRunId())
	if err := os.MkdirAll(artifactDirectory, 0o700); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	buildImages := deps.buildImages
	if buildImages == nil {
		buildImages = func(c context.Context) (evals.BuiltImages, error) {
			return evals.BuildImages(c)
		}
	}
	images, err := buildImages(ctx)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if images.WithDocs.Id == images.WithoutDocs.Id {
		fmt.Fprintln(stderr, "Documentation variants resolved to the same image.")
		return 1
	}

	dctx := evals.CreateDockerContext(images, artifactDirectory, authPath, cli.Provider, cli.Model, cli.RunsPerVariant)
	discoverFn := deps.discover
	if discoverFn == nil {
		discoverFn = evals.DiscoverCases
	}
	runTaskFn := deps.runTask
	if runTaskFn == nil {
		runTaskFn = evals.RunTask
	}

	var discoveries [][]evals.DiscoveredEvalCase
	for _, variant := range evals.DocumentationVariants {
		discoveredPath, err := discoverFn(ctx, dctx, variant, files, cli.DiscoveryArgs)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		raw, err := os.ReadFile(discoveredPath)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		parsed, err := jsonx.Parse(raw)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		cases, err := evals.ParseDiscoveredCases(parsed)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		discoveries = append(discoveries, cases)
	}
	if err := mainCompareDiscovery(discoveries[0], discoveries[1]); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	cases := make([]evals.DiscoveredEvalCase, 0, len(discoveries[0]))
	protocolCases := make([]mainProtocolCase, 0, len(discoveries[0]))
	for _, evalCase := range discoveries[0] {
		normFile, err := mainNormalizeDiscoveredFile(evalCase.File)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		evalCase.File = normFile
		cases = append(cases, evalCase)
		protocolCases = append(protocolCases, mainProtocolCase{
			EvalSet: evalCase.EvalSet,
			CaseId:  evalCase.CaseId,
			File:    evalCase.File,
		})
	}
	if len(cases) == 0 {
		fmt.Fprintln(stderr, "No documentation eval cases matched the selection.")
		return 1
	}

	tasks, err := evals.CreateTaskPlan(cases, modelIdentity, cli.RunsPerVariant)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	protocol := mainProtocolPayload{
		SchemaVersion:  1,
		Model:          modelIdentity,
		RunsPerVariant: cli.RunsPerVariant,
		Images:         images,
		Files:          files,
		Cases:          protocolCases,
		Tasks:          tasks,
	}
	protocolBytes, err := jsonx.Marshal(protocol)
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	digestBytes := sha256.Sum256(protocolBytes)
	protocolDigest := hex.EncodeToString(digestBytes[:])

	protocolOut, err := jsonx.MarshalIndent(mainProtocolFile{
		mainProtocolPayload: protocol,
		ProtocolDigest:      protocolDigest,
	}, "  ")
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "protocol.json"), append(protocolOut, '\n'), 0o600); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	expectedRunsOut, err := jsonx.MarshalIndent(tasks, "  ")
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "expected-runs.json"), append(expectedRunsOut, '\n'), 0o600); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}

	observations := make([]evals.EvalObservation, 0, len(tasks))
	for idx, task := range tasks {
		fmt.Fprintf(stdout, "\n[%d/%d] %s > %s | %s | %s | run %d\n",
			idx+1, len(tasks), task.EvalSet, task.CaseId, task.Variant, task.Model, task.RunNumber)
		reportPath, err := runTaskFn(ctx, dctx, task)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		var obs evals.EvalObservation
		if reportPath != "" {
			obs, err = evals.ReadTaskObservation(task, reportPath, artifactDirectory)
			if err != nil {
				fmt.Fprintln(stderr, err.Error())
				return 1
			}
		} else {
			obs = evals.ErroredObservation(task)
		}
		observations = append(observations, obs)

		lines := make([]string, 0, len(observations))
		for _, item := range observations {
			s, err := jsonx.Stringify(item)
			if err != nil {
				fmt.Fprintln(stderr, err.Error())
				return 1
			}
			lines = append(lines, s)
		}
		if err := os.WriteFile(
			filepath.Join(artifactDirectory, "observations.jsonl"),
			[]byte(strings.Join(lines, "\n")+"\n"),
			0o600,
		); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
	}

	expectedRuns := make([]evals.ExpectedEvalRun, len(tasks))
	for i, task := range tasks {
		expectedRuns[i] = evals.ExpectedEvalRun{
			EvalSet:   task.EvalSet,
			CaseId:    task.CaseId,
			Variant:   task.Variant,
			Model:     task.Model,
			RunNumber: task.RunNumber,
		}
	}
	report := evals.SummarizeEvalObservations(protocolDigest, expectedRuns, observations)
	reportText := evals.FormatEvalComparisonReport(report)
	reportJSON, err := jsonx.MarshalIndent(report, "  ")
	if err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "report.json"), append(reportJSON, '\n'), 0o600); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	if err := os.WriteFile(filepath.Join(artifactDirectory, "report.txt"), []byte(reportText+"\n"), 0o600); err != nil {
		fmt.Fprintln(stderr, err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "\n%s\n", reportText)
	fmt.Fprintf(stdout, "\nArtifacts: %s\n", artifactDirectory)
	if len(report.BlockedPairs) > 0 {
		return 1
	}
	return 0
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := mainRun(ctx, os.Args[1:], mainDeps{})
	utils.ConfigExit(code)
}
