// Ported from packages/coding-agent/test/experimental-remote-runtime.test.ts (pi v1.0.0).

package services_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/keejkrej/pi-go/ai"
	"github.com/keejkrej/pi-go/ai/providers"
	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/client"
	"github.com/keejkrej/pi-go/codingagent/cli/experimentalcli"
	"github.com/keejkrej/pi-go/codingagent/experimental"
	"github.com/keejkrej/pi-go/codingagent/experimental/plugins"
	"github.com/keejkrej/pi-go/codingagent/experimental/services"
	"github.com/keejkrej/pi-go/codingagent/internal/testkit"
	"github.com/keejkrej/pi-go/durable"
	"github.com/keejkrej/pi-go/durable/storage/sqlite"
	"github.com/keejkrej/pi-go/internal/jsonx"
	"github.com/keejkrej/pi-go/protocol"
)

const (
	terrFauxWorkerEnv  = "PI_TEST_FAUX_SESSION_WORKER"
	terrMultiPluginEnv = "PI_TEST_MULTI_PLUGIN_FACETS"
	terrDefaultProv    = "anthropic"
	terrDefaultModel   = "claude-sonnet-4-5"
)

type terrKeyedProbeState struct {
	Value string `json:"value"`
}

type terrKeyedProbe interface {
	State() chord.ReplicatedState[terrKeyedProbeState]
	Replace(value string, ctx chord.Context) error
	Wait(ctx chord.Context) error
}

type terrGreetRequest struct {
	Name string `json:"name"`
}

type terrGreetResult struct {
	Greeting          string `json:"greeting"`
	WorkerActivations int    `json:"workerActivations"`
}

type terrExampleFacetService interface {
	Greet(req terrGreetRequest, ctx chord.Context) (terrGreetResult, error)
}

type terrSecondPluginService interface {
	Read(ctx chord.Context) (string, error)
}

var (
	terrKeyedProbeService = func() *chord.Service[terrKeyedProbe] {
		s, err := chord.DefineService[terrKeyedProbe]("test.keyed-probe", nil)
		if err != nil {
			panic(err)
		}
		return s
	}()
	terrExampleService = func() *chord.Service[terrExampleFacetService] {
		s, err := chord.DefineService[terrExampleFacetService]("@earendil-works/pi-example-plugin.service", nil)
		if err != nil {
			panic(err)
		}
		return s
	}()
	terrSecondService = func() *chord.Service[terrSecondPluginService] {
		s, err := chord.DefineService[terrSecondPluginService]("test.second-plugin", nil)
		if err != nil {
			panic(err)
		}
		return s
	}()
)

type terrServiceView interface {
	Get(property any) (any, error)
	Call(member string, args ...any) (any, error)
}

type terrStateValueReader interface {
	StateValue() (any, bool, error)
}

func TestMain(m *testing.M) {
	if os.Getenv(terrFauxWorkerEnv) == "1" && os.Getenv(experimental.InternalProcessEnv) == string(experimental.InternalRoleSessionWorker) {
		_, ok, err := experimental.ConsumeInternalProcessRole()
		if err != nil || !ok {
			_, _ = fmt.Fprintf(os.Stderr, "faux session worker role error: ok=%v err=%v\n", ok, err)
			os.Exit(1)
		}
		args, err := experimental.ConsumeInternalProcessArgs(os.Args[1:])
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := experimental.RunSessionWorkerWithHarness(args, terrCreateFauxWorkerHarness); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	if handled, err := experimental.MaybeRunInternalProcess(os.Args[1:]); handled {
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func terrCreateFauxWorkerHarness(databasePath string, options experimental.SessionWorkerOptions) (*services.SessionWorkerRuntime, error) {
	if options.Provider != terrDefaultProv || options.Model != terrDefaultModel {
		return nil, fmt.Errorf("Unexpected faux worker model: %s/%s", options.Provider, options.Model)
	}
	faux, err := providers.FauxProvider(nil)
	if err != nil {
		return nil, err
	}
	answer1 := providers.FauxAssistantMessage(providers.FauxAssistantText("deterministic remote answer"), nil)
	answer2 := providers.FauxAssistantMessage(providers.FauxAssistantText("deterministic remote answer"), nil)
	faux.SetResponses([]providers.FauxResponseStep{
		providers.FauxResponseMessage{Message: answer1},
		providers.FauxResponseMessage{Message: answer2},
	})

	models := ai.CreateModels(nil)
	models.SetProvider(faux.Provider)

	store, err := sqlite.OpenNodeSqliteStorage(databasePath)
	if err != nil {
		return nil, err
	}
	harness, err := durable.HarnessOpen(store, durable.HarnessOptions{
		Models:   models,
		Registry: durable.CreateRegistry(),
	}, chord.BackgroundContext)
	if err != nil {
		_ = store.Close(chord.BackgroundContext)
		return nil, err
	}
	model := faux.GetModel()
	conversation, err := harness.Root(chord.BackgroundContext, durable.RootOptions{
		Agent: &durable.AgentChange{
			Cwd: jsonx.Some(options.Metadata.Cwd),
			Model: jsonx.Some(durable.ModelRef{
				Provider: string(model.Provider),
				ModelId:  model.Id,
			}),
		},
	})
	if err != nil {
		_ = harness.Close(chord.BackgroundContext)
		return nil, err
	}

	facets := []chord.Facet{
		chord.DefineFacet(chord.Facet{
			Id: "@test/keyed-probe",
			Setup: func(env chord.FacetEnvironment) error {
				probes := env.ProvideMany(terrKeyedProbeService).(chord.ServiceSpawner[any])
				var spawn func(value string)
				spawn = func(value string) {
					state := env.ReplicatedState(terrKeyedProbeState{Value: value})
					var closeFn func()
					closeFn = probes.Spawn("probe", map[string]any{
						"state": state,
						"replace": func(next string, _ chord.Context) error {
							if closeFn != nil {
								closeFn()
							}
							spawn(next)
							return nil
						},
						"wait": func(ctx chord.Context) error {
							if ctx == nil || ctx.AbortSignal() == nil {
								return errors.New("Probe wait requires cancellation")
							}
							<-ctx.AbortSignal().Done()
							if err := ctx.AbortSignal().Err(); err != nil {
								return err
							}
							return context.Canceled
						},
					})
				}
				env.OnActivate(func() error {
					spawn("first")
					return nil
				})
				return nil
			},
		}),
	}
	if os.Getenv(terrMultiPluginEnv) == "1" {
		activations := 0
		facets = append(facets,
			chord.DefineFacet(chord.Facet{
				Id: "@earendil-works/pi-example-plugin/session",
				Setup: func(env chord.FacetEnvironment) error {
					env.OnActivate(func() error {
						activations++
						return nil
					})
					env.Provide(terrExampleService, map[string]any{
						"greet": func(rawReq any, _ chord.Context) (terrGreetResult, error) {
							var req terrGreetRequest
							if err := jsonx.Decode(rawReq, &req); err != nil {
								return terrGreetResult{}, err
							}
							return terrGreetResult{
								Greeting:          fmt.Sprintf("Hello, %s", req.Name),
								WorkerActivations: activations,
							}, nil
						},
					})
					return nil
				},
			}),
			chord.DefineFacet(chord.Facet{
				Id: "second-session-plugin",
				Setup: func(env chord.FacetEnvironment) error {
					env.Provide(terrSecondService, map[string]any{
						"read": func(_ chord.Context) (string, error) {
							return "second", nil
						},
					})
					return nil
				},
			}),
		)
	}

	return &services.SessionWorkerRuntime{
		Harness:      harness,
		Conversation: conversation,
		FacetLoader:  chord.CreateStaticFacetLoader(facets),
	}, nil
}

type terrEnv struct {
	t        *testing.T
	agentDir string
	mu       sync.Mutex
	servers  []*experimental.RunningServer
	clients  []*client.Client
}

func terrNewEnv(t *testing.T) *terrEnv {
	t.Helper()
	agentDir := terrMkdirTemp(t, "pa-")
	if err := testkit.ConfigureExperimentalWorkerModel(agentDir); err != nil {
		t.Fatalf("ConfigureExperimentalWorkerModel: %v", err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	if _, err := testkit.CreateExperimentalSessions(filepath.Join(agentDir, "experimental", "sessions"), []string{"demo-1", "demo-2"}); err != nil {
		t.Fatalf("CreateExperimentalSessions: %v", err)
	}
	env := &terrEnv{
		t:        t,
		agentDir: agentDir,
	}
	t.Cleanup(env.cleanup)
	return env
}

func (e *terrEnv) cleanup() {
	e.mu.Lock()
	cls := append([]*client.Client(nil), e.clients...)
	srvs := append([]*experimental.RunningServer(nil), e.servers...)
	e.clients = nil
	e.servers = nil
	e.mu.Unlock()

	for _, c := range cls {
		_ = c.Dispose()
	}
	for _, s := range srvs {
		_ = s.Close()
	}
}

func (e *terrEnv) trackServer(s *experimental.RunningServer) {
	e.mu.Lock()
	e.servers = append(e.servers, s)
	e.mu.Unlock()
}

func (e *terrEnv) trackClient(c *client.Client) {
	e.mu.Lock()
	e.clients = append(e.clients, c)
	e.mu.Unlock()
}

func (e *terrEnv) untrackClient(c *client.Client) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, candidate := range e.clients {
		if candidate == c {
			e.clients = append(e.clients[:i], e.clients[i+1:]...)
			return
		}
	}
}

func (e *terrEnv) makeServer() (string, *experimental.RunningServer) {
	e.t.Helper()
	dir := terrMkdirTemp(e.t, "pes-")
	srv, err := experimental.StartServer(experimental.StartServerOptions{
		Directory: dir,
		Provider:  terrDefaultProv,
		Model:     terrDefaultModel,
	})
	if err != nil {
		e.t.Fatalf("StartServer: %v", err)
	}
	e.trackServer(srv)
	return dir, srv
}

func (e *terrEnv) attachClient(srv *experimental.RunningServer, sessionId string) *client.Client {
	e.t.Helper()
	factory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: srv.SocketPath})
	if err != nil {
		e.t.Fatalf("CreateUnixTransportFactory: %v", err)
	}
	c, err := client.Connect(client.ClientOptions{
		ServerId:         srv.ServerId,
		TransportFactory: factory,
	})
	if err != nil {
		e.t.Fatalf("client.Connect: %v", err)
	}
	e.trackClient(c)
	terrAttachSession(e.t, c, sessionId)
	return c
}

func terrAttachSession(t *testing.T, c *client.Client, sessionId string) {
	t.Helper()
	binding := testkit.CreateServerServiceBinding(c, testkit.ExperimentalServiceBindingOptions{
		Services: []chord.ServiceRef{{Id: services.SessionManagementService.Id}},
	})
	defer func() {
		_ = binding.Dispose(chord.BackgroundContext)
	}()
	if err := binding.Ready(chord.BackgroundContext); err != nil {
		t.Fatalf("server binding Ready: %v", err)
	}
	mgmt := services.AsSessionManagement(binding.Use(services.SessionManagementService))
	if err := mgmt.Attach(sessionId, chord.BackgroundContext); err != nil {
		t.Fatalf("Attach(%q): %v", sessionId, err)
	}
}

func terrMkdirTemp(t *testing.T, prefix string) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", prefix)
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	t.Cleanup(func() {
		_ = os.RemoveAll(dir)
	})
	return dir
}

func terrWriteFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile(%s): %v", path, err)
	}
}

func terrPathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func terrProcessExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func terrWaitFor(t *testing.T, desc string, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func terrWritePluginPackage(t *testing.T, dir string, withSession bool) {
	t.Helper()
	terrWriteFile(t, filepath.Join(dir, "package.json"), `{"name":"@earendil-works/pi-example-plugin","version":"1.0.0","peerDependencies":{"@earendil-works/chord":"^0.84.4","@earendil-works/pi-coding-agent":"^0.84.4"}}`+"\n")
	terrWriteFile(t, filepath.Join(dir, "src", "tui.ts"), "import { defineFacet } from \"@earendil-works/chord\"; export default defineFacet({ id: \"@earendil-works/pi-example-plugin/tui\", setup() {} });\n")
	if withSession {
		terrWriteFile(t, filepath.Join(dir, "src", "session.ts"), "import { defineFacet } from \"@earendil-works/chord\"; export default defineFacet({ id: \"@earendil-works/pi-example-plugin/session\", setup() {} });\n")
	}
}

func terrPtr[T any](v T) *T {
	return &v
}

func terrHasSessionId(sessions []services.SessionSummary, target string) bool {
	for _, s := range sessions {
		if s.SessionId == target {
			return true
		}
	}
	return false
}

func terrSessionIds(sessions []services.SessionSummary) []string {
	ids := make([]string, 0, len(sessions))
	for _, s := range sessions {
		ids = append(ids, s.SessionId)
	}
	return ids
}

func terrReadProbeValue(t *testing.T, raw any) (string, bool) {
	t.Helper()
	view, ok := raw.(terrServiceView)
	if !ok {
		t.Fatalf("expected terrServiceView for KeyedProbe, got %T", raw)
	}
	stateRaw, err := view.Get("state")
	if err != nil {
		return "", false
	}
	reader, ok := stateRaw.(terrStateValueReader)
	if !ok {
		return "", false
	}
	val, present, err := reader.StateValue()
	if err != nil || !present {
		return "", false
	}
	var decoded terrKeyedProbeState
	if err := jsonx.Decode(val, &decoded); err != nil {
		return "", false
	}
	return decoded.Value, true
}

func terrCallProbeReplace(raw any, next string, ctx chord.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
			} else {
				err = fmt.Errorf("%v", r)
			}
		}
	}()
	view, ok := raw.(terrServiceView)
	if !ok {
		return fmt.Errorf("unexpected KeyedProbe type %T", raw)
	}
	_, err = view.Call("replace", next, ctx)
	return err
}

func TestExperimentalRemoteRuntime(t *testing.T) {
	t.Run("uses PI_SERVER_DIR and PI_SERVER_ID", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "psd-")
		serverId := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		t.Setenv("PI_SERVER_DIR", dir)
		t.Setenv("PI_SERVER_ID", string(serverId))

		srv, err := experimental.StartServer()
		if err != nil {
			t.Fatalf("StartServer: %v", err)
		}
		env.trackServer(srv)

		if srv.ServerId != serverId {
			t.Fatalf("ServerId = %q, want %q", srv.ServerId, serverId)
		}
		wantSocket := filepath.Join(dir, fmt.Sprintf("%s.sock", serverId))
		if srv.SocketPath != wantSocket {
			t.Fatalf("SocketPath = %q, want %q", srv.SocketPath, wantSocket)
		}
		dirInfo, err := os.Lstat(dir)
		if err != nil {
			t.Fatalf("Lstat(dir): %v", err)
		}
		if dirInfo.Mode().Perm() != 0o700 {
			t.Fatalf("dir perm = %o, want 700", dirInfo.Mode().Perm())
		}
		pubInfo, err := os.Lstat(srv.SocketPath)
		if err != nil {
			t.Fatalf("Lstat(publicSock): %v", err)
		}
		ctrlInfo, err := os.Lstat(filepath.Join(dir, fmt.Sprintf("control-%s.sock", srv.ServerId)))
		if err != nil {
			t.Fatalf("Lstat(controlSock): %v", err)
		}
		if pubInfo.Mode()&os.ModeSocket == 0 || pubInfo.Mode().Perm() != 0o600 {
			t.Fatalf("publicSock mode=%v perm=%o", pubInfo.Mode(), pubInfo.Mode().Perm())
		}
		if ctrlInfo.Mode()&os.ModeSocket == 0 || ctrlInfo.Mode().Perm() != 0o600 {
			t.Fatalf("controlSock mode=%v perm=%o", ctrlInfo.Mode(), ctrlInfo.Mode().Perm())
		}

		dirEntries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		var names []string
		for _, ent := range dirEntries {
			names = append(names, ent.Name())
		}
		if len(names) != 3 {
			t.Fatalf("len(entries) = %d (%v), want 3", len(names), names)
		}
		for _, name := range names {
			if strings.HasPrefix(name, ".") {
				t.Fatalf("unexpected hidden entry %q", name)
			}
		}
		if !slices.Contains(names, fmt.Sprintf("%s.sock", serverId)) {
			t.Fatalf("missing public socket in %v", names)
		}
		if !slices.Contains(names, fmt.Sprintf("control-%s.sock", serverId)) {
			t.Fatalf("missing control socket in %v", names)
		}
		serverSockPat := regexp.MustCompile(`^server-` + regexp.QuoteMeta(string(serverId)) + `-[0-9a-f]{12}\.sock$`)
		if !slices.ContainsFunc(names, serverSockPat.MatchString) {
			t.Fatalf("missing versioned server socket in %v", names)
		}

		res, err := experimental.RunClient(experimentalcli.ClientCommand{Command: "client"})
		if err != nil {
			t.Fatalf("RunClient: %v", err)
		}
		if res.Kind != experimental.ClientResultKindList || len(res.Sessions) != 2 || res.Sessions[0].SessionId != "demo-1" || res.Sessions[1].SessionId != "demo-2" {
			t.Fatalf("RunClient list = %+v", res)
		}
	})

	t.Run("rejects a provider without a model", func(t *testing.T) {
		_ = terrNewEnv(t)
		dir := terrMkdirTemp(t, "pes-")
		_, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
		})
		if err == nil || !strings.Contains(err.Error(), "provider requires a model") {
			t.Fatalf("expected 'provider requires a model', got %v", err)
		}
	})

	t.Run("preserves an existing Session model when the server default changes", func(t *testing.T) {
		env := terrNewEnv(t)
		terrWriteFile(t, filepath.Join(env.agentDir, "settings.json"), `{"defaultProvider":"anthropic","defaultModel":"claude-opus-4-6"}`)
		dir := terrMkdirTemp(t, "pes-")

		first, err := experimental.StartServer(experimental.StartServerOptions{Directory: dir})
		if err != nil {
			t.Fatalf("StartServer(first): %v", err)
		}
		env.trackServer(first)
		c1 := env.attachClient(first, "demo-1")
		_ = c1.Dispose()
		env.untrackClient(c1)
		terrWaitFor(t, "first worker exit", func() bool {
			_, ok := first.WorkerPids()["demo-1"]
			return !ok
		})
		if err := first.Close(); err != nil {
			t.Fatalf("first.Close: %v", err)
		}

		second, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(second): %v", err)
		}
		env.trackServer(second)
		c2 := env.attachClient(second, "demo-1")
		_ = c2.Dispose()
		env.untrackClient(c2)
		terrWaitFor(t, "second worker exit", func() bool {
			_, ok := second.WorkerPids()["demo-1"]
			return !ok
		})

		state, err := testkit.ReadExperimentalSessionState(second.SessionDir, "demo-1")
		if err != nil {
			t.Fatalf("ReadExperimentalSessionState: %v", err)
		}
		if state.Model == nil || state.Model.Provider != "anthropic" || state.Model.ModelId != "claude-opus-4-6" {
			t.Fatalf("preserved model = %+v, want anthropic/claude-opus-4-6", state.Model)
		}
	})

	t.Run("rejects model options when discovery selects an existing server", func(t *testing.T) {
		env := terrNewEnv(t)
		dir, _ := env.makeServer()
		_, err := experimental.RunClient(
			experimentalcli.ClientCommand{Command: "client", Model: terrPtr("anthropic/claude-opus-4-6")},
			experimental.RunClientOptions{Directory: dir},
		)
		if err == nil || !strings.Contains(err.Error(), "Model selection is only valid when automatically activating a new server") {
			t.Fatalf("expected discovery model error, got %v", err)
		}
	})

	t.Run("rechecks an auto-discovered server after a version mismatch", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "pvm-")
		staleId := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		realId := protocol.ServerId("00000000-0000-4000-8000-000000000002")

		staleSock := filepath.Join(dir, fmt.Sprintf("%s.sock", staleId))
		ln, err := net.Listen("unix", staleSock)
		if err != nil {
			t.Fatalf("Listen stale unix socket: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				go func(nc net.Conn) {
					defer func() { _ = nc.Close() }()
					dec, decErr := protocol.NewClientMessageDecoder(nil)
					if decErr != nil {
						return
					}
					buf := make([]byte, 4096)
					for {
						n, rerr := nc.Read(buf)
						if n > 0 {
							msgs, perr := dec.Push(buf[:n])
							if perr != nil {
								return
							}
							if len(msgs) > 0 {
								resp, _ := protocol.EncodeServerMessage(&protocol.ServerHelloError{
									Type: "hello_error",
									Error: protocol.ProtocolError{
										Code:    "version",
										Message: "stale server",
									},
								}, nil)
								_, _ = nc.Write(resp)
								return
							}
						}
						if rerr != nil {
							return
						}
					}
				}(conn)
			}
		}()

		srv, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			ServerId:  &realId,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer: %v", err)
		}
		env.trackServer(srv)

		cr, err := experimental.OpenClientRuntime(
			experimentalcli.ClientCommand{Command: "client"},
			experimental.OpenClientRuntimeOptions{Directory: dir},
		)
		if err != nil {
			t.Fatalf("OpenClientRuntime: %v", err)
		}
		defer func() { _ = cr.Dispose() }()
		if len(cr.Servers) != 1 || cr.Servers[0].Route.ServerId != srv.ServerId {
			t.Fatalf("discovered servers = %+v, want [%s]", cr.Servers, srv.ServerId)
		}
	})

	t.Run("serializes concurrent cold activation and retires after both clients leave", func(t *testing.T) {
		_ = terrNewEnv(t)
		dir := terrMkdirTemp(t, "pas-")
		serverId := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		t.Setenv("PI_SERVER_DIR", dir)
		t.Setenv("PI_SERVER_ID", string(serverId))

		var wg sync.WaitGroup
		results := make([]experimental.ClientResult, 2)
		errs := make([]error, 2)
		for i := range 2 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				results[idx], errs[idx] = experimental.RunClient(experimentalcli.ClientCommand{Command: "client"})
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("RunClient[%d]: %v", i, err)
			}
			r := results[i]
			if r.Kind != experimental.ClientResultKindList || len(r.Sessions) != 2 ||
				r.Sessions[0].ServerId != serverId || r.Sessions[0].SessionId != "demo-1" ||
				r.Sessions[1].ServerId != serverId || r.Sessions[1].SessionId != "demo-2" {
				t.Fatalf("results[%d] = %+v", i, r)
			}
		}

		pubSock := filepath.Join(dir, fmt.Sprintf("%s.sock", serverId))
		ctrlSock := filepath.Join(dir, fmt.Sprintf("control-%s.sock", serverId))
		terrWaitFor(t, "public socket retirement", func() bool { return !terrPathExists(pubSock) })
		terrWaitFor(t, "control socket retirement", func() bool { return !terrPathExists(ctrlSock) })
	})

	t.Run("passes client plugin packages to a cold server and restores them for its next generation", func(t *testing.T) {
		_ = terrNewEnv(t)
		dir := terrMkdirTemp(t, "pap-")
		pkgDir := terrMkdirTemp(t, "ppk-")
		terrWritePluginPackage(t, pkgDir, false)
		serverId := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		t.Setenv("PI_SERVER_DIR", dir)
		t.Setenv("PI_SERVER_ID", string(serverId))

		first, err := experimental.OpenClientRuntime(experimentalcli.ClientCommand{
			Command:  "client",
			Provider: terrPtr(terrDefaultProv),
			Model:    terrPtr(terrDefaultModel),
		})
		if err != nil {
			t.Fatalf("OpenClientRuntime(first): %v", err)
		}
		activatedFirst, err := experimental.ActivateBuiltinClientServices(first.Servers[0])
		if err != nil {
			_ = first.Dispose()
			t.Fatalf("ActivateBuiltinClientServices(first): %v", err)
		}
		presFirst, err := activatedFirst.Plugins.PrepareSession(services.PrepareSessionPluginsRequest{
			SessionId:    "demo-1",
			PackagePaths: []string{pkgDir},
		}, chord.BackgroundContext)
		if err != nil {
			_ = first.Dispose()
			t.Fatalf("PrepareSession(first): %v", err)
		}
		if err := activatedFirst.Management.Attach("demo-1", chord.BackgroundContext); err != nil {
			_ = first.Dispose()
			t.Fatalf("Attach(first): %v", err)
		}
		loadersFirst, err := plugins.CreatePresentationFacetLoaders(presFirst)
		if err != nil || len(loadersFirst) != 1 {
			_ = first.Dispose()
			t.Fatalf("CreatePresentationFacetLoaders(first): loaders=%d err=%v", len(loadersFirst), err)
		}
		if _, loadErr := loadersFirst[0].Load(); loadErr == nil || !strings.Contains(loadErr.Error(), "unsupported") {
			_ = first.Dispose()
			t.Fatalf("expected explicit CJS unsupported error from pure-Go loader, got %v", loadErr)
		}
		_ = first.Dispose()

		pubSock := filepath.Join(dir, fmt.Sprintf("%s.sock", serverId))
		terrWaitFor(t, "first cold server socket removal", func() bool { return !terrPathExists(pubSock) })

		second, err := experimental.OpenClientRuntime(experimentalcli.ClientCommand{
			Command:  "client",
			Provider: terrPtr(terrDefaultProv),
			Model:    terrPtr(terrDefaultModel),
		})
		if err != nil {
			t.Fatalf("OpenClientRuntime(second): %v", err)
		}
		defer func() { _ = second.Dispose() }()
		activatedSecond, err := experimental.ActivateBuiltinClientServices(second.Servers[0])
		if err != nil {
			t.Fatalf("ActivateBuiltinClientServices(second): %v", err)
		}
		presSecond, err := activatedSecond.Plugins.PrepareSession(services.PrepareSessionPluginsRequest{
			SessionId:    "demo-1",
			PackagePaths: nil,
		}, chord.BackgroundContext)
		if err != nil {
			t.Fatalf("PrepareSession(second): %v", err)
		}
		if err := activatedSecond.Management.Attach("demo-1", chord.BackgroundContext); err != nil {
			t.Fatalf("Attach(second): %v", err)
		}
		loadersSecond, err := plugins.CreatePresentationFacetLoaders(presSecond)
		if err != nil || len(loadersSecond) != 1 {
			t.Fatalf("CreatePresentationFacetLoaders(second): loaders=%d err=%v", len(loadersSecond), err)
		}
	})

	t.Run("retires a cold server after its only Session attachment disconnects", func(t *testing.T) {
		_ = terrNewEnv(t)
		dir := terrMkdirTemp(t, "pas2-")
		serverId := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		t.Setenv("PI_SERVER_DIR", dir)
		t.Setenv("PI_SERVER_ID", string(serverId))

		res, err := experimental.RunClient(experimentalcli.ClientCommand{
			Command:   "client",
			SessionId: terrPtr("demo-1"),
			Provider:  terrPtr(terrDefaultProv),
			Model:     terrPtr(terrDefaultModel),
		})
		if err != nil {
			t.Fatalf("RunClient: %v", err)
		}
		if res.Kind != experimental.ClientResultKindAttached || res.ServerId != serverId || res.SessionId != "demo-1" {
			t.Fatalf("RunClient = %+v", res)
		}
		terrWaitFor(t, "public socket retirement", func() bool {
			return !terrPathExists(filepath.Join(dir, fmt.Sprintf("%s.sock", serverId)))
		})
		terrWaitFor(t, "control socket retirement", func() bool {
			return !terrPathExists(filepath.Join(dir, fmt.Sprintf("control-%s.sock", serverId)))
		})
	})

	t.Run("runs and discovers multiple logical servers from one directory", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "pms-")
		firstId := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		secondId := protocol.ServerId("00000000-0000-4000-8000-000000000002")

		first, err := experimental.StartServer(experimental.StartServerOptions{Directory: dir, ServerId: &firstId})
		if err != nil {
			t.Fatalf("StartServer(first): %v", err)
		}
		env.trackServer(first)
		second, err := experimental.StartServer(experimental.StartServerOptions{Directory: dir, ServerId: &secondId})
		if err != nil {
			t.Fatalf("StartServer(second): %v", err)
		}
		env.trackServer(second)

		ctrl1, err := os.Lstat(filepath.Join(dir, fmt.Sprintf("control-%s.sock", firstId)))
		if err != nil || ctrl1.Mode()&os.ModeSocket == 0 {
			t.Fatalf("control socket 1 stat=%v err=%v", ctrl1, err)
		}
		ctrl2, err := os.Lstat(filepath.Join(dir, fmt.Sprintf("control-%s.sock", secondId)))
		if err != nil || ctrl2.Mode()&os.ModeSocket == 0 {
			t.Fatalf("control socket 2 stat=%v err=%v", ctrl2, err)
		}

		res, err := experimental.RunClient(experimentalcli.ClientCommand{Command: "client"}, experimental.RunClientOptions{Directory: dir})
		if err != nil {
			t.Fatalf("RunClient(list both): %v", err)
		}
		wantBoth := []services.SessionAddress{
			{ServerId: firstId, SessionId: "demo-1"},
			{ServerId: firstId, SessionId: "demo-2"},
			{ServerId: secondId, SessionId: "demo-1"},
			{ServerId: secondId, SessionId: "demo-2"},
		}
		if res.Kind != experimental.ClientResultKindList || !slices.Equal(res.Sessions, wantBoth) {
			t.Fatalf("list both = %+v, want %+v", res.Sessions, wantBoth)
		}

		if err := first.Close(); err != nil {
			t.Fatalf("first.Close: %v", err)
		}
		terrWaitFor(t, "first socket removal", func() bool { return !terrPathExists(first.SocketPath) })

		res, err = experimental.RunClient(experimentalcli.ClientCommand{Command: "client"}, experimental.RunClientOptions{Directory: dir})
		if err != nil {
			t.Fatalf("RunClient(list second): %v", err)
		}
		wantSecond := []services.SessionAddress{
			{ServerId: secondId, SessionId: "demo-1"},
			{ServerId: secondId, SessionId: "demo-2"},
		}
		if res.Kind != experimental.ClientResultKindList || !slices.Equal(res.Sessions, wantSecond) {
			t.Fatalf("list second = %+v, want %+v", res.Sessions, wantSecond)
		}
	})

	t.Run("hydrates and mutates server Session services across framed clients", func(t *testing.T) {
		env := terrNewEnv(t)
		_, srv := env.makeServer()

		f1, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: srv.SocketPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory(1): %v", err)
		}
		c1, err := client.Connect(client.ClientOptions{ServerId: srv.ServerId, TransportFactory: f1})
		if err != nil {
			t.Fatalf("Connect(1): %v", err)
		}
		env.trackClient(c1)

		f2, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: srv.SocketPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory(2): %v", err)
		}
		c2, err := client.Connect(client.ClientOptions{ServerId: srv.ServerId, TransportFactory: f2})
		if err != nil {
			t.Fatalf("Connect(2): %v", err)
		}
		env.trackClient(c2)

		var errsMu sync.Mutex
		var svcErrs []error
		recErr := func(err error) {
			errsMu.Lock()
			svcErrs = append(svcErrs, err)
			errsMu.Unlock()
		}
		s1 := testkit.CreateServerServiceBinding(c1, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{
				{Id: services.SessionDirectoryService.Id},
				{Id: services.SessionManagementService.Id},
			},
			OnError: recErr,
		})
		s2 := testkit.CreateServerServiceBinding(c2, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{
				{Id: services.SessionDirectoryService.Id},
				{Id: services.SessionManagementService.Id},
			},
			OnError: recErr,
		})
		defer func() {
			_ = s1.Dispose(chord.BackgroundContext)
			_ = s2.Dispose(chord.BackgroundContext)
		}()

		st1, ok1 := s1.Connection().Value()
		st2, ok2 := s2.Connection().Value()
		if !ok1 || st1.Status != "connected" || !ok2 || st2.Status != "connected" {
			t.Fatalf("connections = %+v (%v) / %+v (%v)", st1, ok1, st2, ok2)
		}
		cat, err := s1.Catalogue(chord.BackgroundContext)
		if err != nil {
			t.Fatalf("Catalogue: %v", err)
		}
		wantCat := []chord.ServiceCatalogueEntry{
			{ServiceId: services.SessionDirectoryService.Id, Mode: chord.ServiceModeSingleton},
			{ServiceId: services.SessionManagementService.Id, Mode: chord.ServiceModeSingleton},
			{ServiceId: services.PresentationPluginsService.Id, Mode: chord.ServiceModeSingleton},
		}
		if !slices.Equal(cat, wantCat) {
			t.Fatalf("Catalogue = %+v, want %+v", cat, wantCat)
		}

		d1 := services.AsSessionDirectory(s1.Use(services.SessionDirectoryService))
		d2 := services.AsSessionDirectory(s2.Use(services.SessionDirectoryService))
		m1 := services.AsSessionManagement(s1.Use(services.SessionManagementService))
		m2 := services.AsSessionManagement(s2.Use(services.SessionManagementService))

		if err := s1.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("s1.Ready: %v", err)
		}
		if err := s2.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("s2.Ready: %v", err)
		}
		dv1, _ := d1.State().Value()
		dv2, _ := d2.State().Value()
		if !slices.Equal(terrSessionIds(dv1.Sessions), []string{"demo-1", "demo-2"}) {
			t.Fatalf("initial sessions = %v", terrSessionIds(dv1.Sessions))
		}
		if !slices.Equal(terrSessionIds(dv2.Sessions), terrSessionIds(dv1.Sessions)) {
			t.Fatalf("d2 != d1: %v vs %v", dv2.Sessions, dv1.Sessions)
		}

		demo3 := "demo-3"
		if _, err := m1.Create(services.SessionCreateOptions{Id: &demo3}, chord.BackgroundContext); err != nil {
			t.Fatalf("m1.Create(demo-3): %v", err)
		}
		terrWaitFor(t, "demo-3 replicated to both clients", func() bool {
			v1, _ := d1.State().Value()
			v2, _ := d2.State().Value()
			return terrHasSessionId(v1.Sessions, "demo-3") && slices.Equal(terrSessionIds(v1.Sessions), terrSessionIds(v2.Sessions))
		})

		if err := m1.Attach("demo-1", chord.BackgroundContext); err != nil {
			t.Fatalf("m1.Attach: %v", err)
		}
		if err := m2.Attach("demo-1", chord.BackgroundContext); err != nil {
			t.Fatalf("m2.Attach: %v", err)
		}
		if att1 := c1.Attachment(); att1 == nil || att1.SessionId != "demo-1" {
			t.Fatalf("c1.Attachment = %+v", att1)
		}
		if att2 := c2.Attachment(); att2 == nil || att2.SessionId != "demo-1" {
			t.Fatalf("c2.Attachment = %+v", att2)
		}

		if err := m1.Remove("demo-1", chord.BackgroundContext); err != nil {
			t.Fatalf("m1.Remove(demo-1): %v", err)
		}
		terrWaitFor(t, "demo-1 removal and detach on both clients", func() bool {
			v1, _ := d1.State().Value()
			v2, _ := d2.State().Value()
			return c1.Attachment() == nil && c2.Attachment() == nil &&
				!terrHasSessionId(v1.Sessions, "demo-1") &&
				slices.Equal(terrSessionIds(v1.Sessions), terrSessionIds(v2.Sessions))
		})
		if err := m2.Detach(chord.BackgroundContext); err != nil {
			t.Fatalf("m2.Detach: %v", err)
		}
		errsMu.Lock()
		gotErrs := append([]error(nil), svcErrs...)
		errsMu.Unlock()
		if len(gotErrs) != 0 {
			t.Fatalf("unexpected service errors: %v", gotErrs)
		}
	})

	t.Run("hydrates and updates the Models service across concurrent framed clients", func(t *testing.T) {
		env := terrNewEnv(t)
		_, srv := env.makeServer()

		c1 := env.attachClient(srv, "demo-1")
		workerPid, ok := srv.WorkerPids()["demo-1"]
		if !ok || workerPid <= 0 {
			t.Fatalf("expected workerPid for demo-1, got %v", srv.WorkerPids())
		}
		c2 := env.attachClient(srv, "demo-1")
		if got := srv.WorkerPids()["demo-1"]; got != workerPid {
			t.Fatalf("expected reused workerPid %d, got %d", workerPid, got)
		}

		var errsMu sync.Mutex
		var svcErrs []error
		recErr := func(err error) {
			errsMu.Lock()
			svcErrs = append(svcErrs, err)
			errsMu.Unlock()
		}
		s1 := testkit.CreateSessionServiceBinding(c1, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{{Id: services.ModelsService.Id}},
			OnError:  recErr,
		})
		s2 := testkit.CreateSessionServiceBinding(c2, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{{Id: services.ModelsService.Id}},
			OnError:  recErr,
		})
		defer func() {
			_ = s1.Dispose(chord.BackgroundContext)
			_ = s2.Dispose(chord.BackgroundContext)
		}()

		m1 := services.AsModels(s1.Use(services.ModelsService))
		m2 := services.AsModels(s2.Use(services.ModelsService))
		if err := s1.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("s1.Ready: %v", err)
		}
		if err := s2.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("s2.Ready: %v", err)
		}

		a1, _ := s1.Attachment().Value()
		a2, _ := s2.Attachment().Value()
		if a1.Status != "attached" || a1.SessionId != "demo-1" || a2.Status != "attached" || a2.SessionId != "demo-1" {
			t.Fatalf("attachments = %+v / %+v", a1, a2)
		}
		ms1, ok := m1.State().Value()
		if !ok || ms1.Configuration.Model == nil || ms1.Configuration.Model.Provider != terrDefaultProv || ms1.Configuration.Model.ModelId != terrDefaultModel {
			t.Fatalf("m1.State = %+v (%v)", ms1, ok)
		}
		ms2, _ := m2.State().Value()
		if !reflect.DeepEqual(ms2.Configuration, ms1.Configuration) {
			t.Fatalf("m2 config = %+v, want %+v", ms2.Configuration, ms1.Configuration)
		}
		prevThinking := ms1.Configuration.ThinkingLevel
		if err := m1.CycleThinking(chord.BackgroundContext); err != nil {
			t.Fatalf("CycleThinking: %v", err)
		}
		terrWaitFor(t, "CycleThinking replicated across both clients", func() bool {
			v1, ok1 := m1.State().Value()
			v2, ok2 := m2.State().Value()
			return ok1 && ok2 && v1.Configuration.ThinkingLevel != prevThinking && v2.Configuration.ThinkingLevel == v1.Configuration.ThinkingLevel
		})
		errsMu.Lock()
		gotErrs := append([]error(nil), svcErrs...)
		errsMu.Unlock()
		if len(gotErrs) != 0 {
			t.Fatalf("unexpected service errors: %v", gotErrs)
		}
	})

	t.Run("loads conventional Session facets from multiple configured plugin packages", func(t *testing.T) {
		env := terrNewEnv(t)

		// First verify pure-Go plugin package builds both configured packages and reports CJS session facets as unsupported without panicking.
		pkgRoot := terrMkdirTemp(t, "pmp-")
		pkgOne := filepath.Join(pkgRoot, "pi-example-plugin")
		pkgTwo := filepath.Join(pkgRoot, "second-plugin")
		terrWritePluginPackage(t, pkgOne, true)
		terrWriteFile(t, filepath.Join(pkgTwo, "package.json"), `{"name":"@earendil-works/second-session-plugin","version":"1.0.0","peerDependencies":{"@earendil-works/chord":"^0.84.4"}}`+"\n")
		terrWriteFile(t, filepath.Join(pkgTwo, "src", "session.ts"), "import { defineFacet, defineService } from \"@earendil-works/chord\"; const Service = defineService(\"test.second-plugin\"); export default defineFacet({ id: \"second-session-plugin\", setup(env) { env.provide(Service, { async read() { return \"second\"; } }); } });\n")

		_, srvCjs := env.makeServer()
		crCjs, err := experimental.OpenClientRuntime(
			experimentalcli.ClientCommand{
				Command: "client",
				Connect: &experimentalcli.TransportAddress{Transport: experimentalcli.TransportTypeUnix, Path: srvCjs.SocketPath},
			},
		)
		if err != nil {
			t.Fatalf("OpenClientRuntime(cjs check): %v", err)
		}
		actCjs, err := experimental.ActivateBuiltinClientServices(crCjs.Servers[0])
		if err != nil {
			_ = crCjs.Dispose()
			t.Fatalf("ActivateBuiltinClientServices(cjs check): %v", err)
		}
		if _, err := actCjs.Plugins.PrepareSession(services.PrepareSessionPluginsRequest{
			SessionId:    "demo-1",
			PackagePaths: []string{pkgOne, pkgTwo},
		}, chord.BackgroundContext); err != nil {
			_ = crCjs.Dispose()
			t.Fatalf("PrepareSession(multi CJS packages): %v", err)
		}
		if err := actCjs.Management.Attach("demo-1", chord.BackgroundContext); err == nil || !strings.Contains(err.Error(), "unsupported") {
			_ = crCjs.Dispose()
			t.Fatalf("expected explicit pure-Go CJS unsupported error on Attach, got %v", err)
		}
		_ = crCjs.Dispose()

		// Now verify multiple native Go session facets load and serve concurrently over the framed transport.
		t.Setenv(terrFauxWorkerEnv, "1")
		t.Setenv(terrMultiPluginEnv, "1")
		_, srv := env.makeServer()
		cr, err := experimental.OpenClientRuntime(
			experimentalcli.ClientCommand{
				Command: "client",
				Connect: &experimentalcli.TransportAddress{Transport: experimentalcli.TransportTypeUnix, Path: srv.SocketPath},
			},
		)
		if err != nil {
			t.Fatalf("OpenClientRuntime(native multi): %v", err)
		}
		defer func() { _ = cr.Dispose() }()

		act, err := experimental.ActivateBuiltinClientServices(cr.Servers[0])
		if err != nil {
			t.Fatalf("ActivateBuiltinClientServices(native multi): %v", err)
		}
		if err := act.Management.Attach("demo-1", chord.BackgroundContext); err != nil {
			t.Fatalf("Attach(demo-1): %v", err)
		}

		binding := testkit.CreateSessionServiceBinding(cr.Servers[0].Client, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{
				{Id: terrExampleService.Id},
				{Id: terrSecondService.Id},
			},
		})
		defer func() { _ = binding.Dispose(chord.BackgroundContext) }()
		if err := binding.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("binding.Ready: %v", err)
		}
		exView := binding.Use(terrExampleService).(terrServiceView)
		rawGreet, err := exView.Call("greet", terrGreetRequest{Name: "Armin"}, chord.BackgroundContext)
		if err != nil {
			t.Fatalf("greet: %v", err)
		}
		var greetRes terrGreetResult
		if err := jsonx.Decode(rawGreet, &greetRes); err != nil {
			t.Fatalf("decode greet result: %v", err)
		}
		if greetRes.WorkerActivations != 1 {
			t.Fatalf("WorkerActivations = %d, want 1", greetRes.WorkerActivations)
		}
		secView := binding.Use(terrSecondService).(terrServiceView)
		rawRead, err := secView.Call("read", chord.BackgroundContext)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if s, ok := rawRead.(string); !ok || s != "second" {
			t.Fatalf("second plugin read = %#v, want \"second\"", rawRead)
		}
	})

	t.Run("uses the most recently selected model for a new Session", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "pmd-")
		srv, err := experimental.StartServer(experimental.StartServerOptions{Directory: dir})
		if err != nil {
			t.Fatalf("StartServer: %v", err)
		}
		env.trackServer(srv)

		c1 := env.attachClient(srv, "demo-1")
		s1 := testkit.CreateSessionServiceBinding(c1, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{{Id: services.ModelsService.Id}},
		})
		m1 := services.AsModels(s1.Use(services.ModelsService))
		if err := s1.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("s1.Ready: %v", err)
		}
		if err := m1.Select(services.ModelRef{Provider: "anthropic", ModelId: "claude-opus-4-6"}, chord.BackgroundContext); err != nil {
			t.Fatalf("m1.Select: %v", err)
		}
		_ = s1.Dispose(chord.BackgroundContext)
		_ = c1.Dispose()
		env.untrackClient(c1)
		terrWaitFor(t, "demo-1 worker retired", func() bool {
			_, ok := srv.WorkerPids()["demo-1"]
			return !ok
		})

		c2 := env.attachClient(srv, "demo-2")
		s2 := testkit.CreateSessionServiceBinding(c2, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{{Id: services.ModelsService.Id}},
		})
		defer func() { _ = s2.Dispose(chord.BackgroundContext) }()
		m2 := services.AsModels(s2.Use(services.ModelsService))
		if err := s2.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("s2.Ready: %v", err)
		}
		st2, ok := m2.State().Value()
		if !ok || st2.Configuration.Model == nil || st2.Configuration.Model.Provider != "anthropic" || st2.Configuration.Model.ModelId != "claude-opus-4-6" {
			t.Fatalf("demo-2 model = %+v (%v), want anthropic/claude-opus-4-6", st2.Configuration.Model, ok)
		}
	})

	t.Run("composes management attachment with Session service hydration", func(t *testing.T) {
		env := terrNewEnv(t)
		_, srv := env.makeServer()
		cr, err := experimental.OpenClientRuntime(experimentalcli.ClientCommand{
			Command: "client",
			Connect: &experimentalcli.TransportAddress{Transport: experimentalcli.TransportTypeUnix, Path: srv.SocketPath},
		})
		if err != nil {
			t.Fatalf("OpenClientRuntime: %v", err)
		}
		defer func() { _ = cr.Dispose() }()

		act, err := experimental.ActivateBuiltinClientServices(cr.Servers[0])
		if err != nil {
			t.Fatalf("ActivateBuiltinClientServices: %v", err)
		}
		if _, err := act.Plugins.PrepareSession(services.PrepareSessionPluginsRequest{SessionId: "demo-1"}, chord.BackgroundContext); err != nil {
			t.Fatalf("PrepareSession: %v", err)
		}
		if err := act.Management.Attach("demo-1", chord.BackgroundContext); err != nil {
			t.Fatalf("Attach(demo-1): %v", err)
		}

		att, ok := act.Session.Attachment().Value()
		if !ok || att.Status != "attached" || att.SessionId != "demo-1" {
			t.Fatalf("attachment = %+v (%v)", att, ok)
		}
		ms, ok := act.Models.State().Value()
		if !ok || ms.Configuration.Model == nil || ms.Configuration.Model.Provider != terrDefaultProv || ms.Configuration.Model.ModelId != terrDefaultModel {
			t.Fatalf("models = %+v (%v)", ms.Configuration.Model, ok)
		}
	})

	t.Run("fences superseded attachment hydration by attachment generation", func(t *testing.T) {
		env := terrNewEnv(t)
		_, srv := env.makeServer()

		baseFactory, err := client.CreateUnixTransportFactory(client.UnixTransportOptions{Path: srv.SocketPath})
		if err != nil {
			t.Fatalf("CreateUnixTransportFactory: %v", err)
		}

		delayed := make(chan struct{})
		var releaseOnce sync.Once
		releaseDelay := func() {
			releaseOnce.Do(func() { close(delayed) })
		}
		defer releaseDelay()

		wrappedFactory := func(handlers client.ByteTransportHandlers) (client.ByteTransport, error) {
			clientDec, err := protocol.NewClientMessageDecoder(nil)
			if err != nil {
				return nil, err
			}
			srvDec, err := protocol.NewServerMessageDecoder(nil)
			if err != nil {
				return nil, err
			}
			var mu sync.Mutex
			var delayedReqId string
			var deliverMu sync.Mutex

			wrappedHandlers := client.ByteTransportHandlers{
				OnData: func(chunk []byte) {
					msgs, decErr := srvDec.Push(chunk)
					if decErr != nil {
						deliverMu.Lock()
						handlers.OnData(chunk)
						deliverMu.Unlock()
						return
					}
					for _, msg := range msgs {
						var id string
						switch m := msg.(type) {
						case *protocol.ResponseSuccess:
							if m != nil {
								id = m.Id
							}
						case *protocol.ResponseFailure:
							if m != nil {
								id = m.Id
							}
						}
						mu.Lock()
						matchDelay := delayedReqId != "" && id == delayedReqId
						mu.Unlock()

						enc, encErr := protocol.EncodeServerMessage(msg, nil)
						if encErr != nil {
							continue
						}
						if matchDelay {
							go func(payload []byte) {
								<-delayed
								deliverMu.Lock()
								handlers.OnData(payload)
								deliverMu.Unlock()
							}(enc)
						} else {
							deliverMu.Lock()
							handlers.OnData(enc)
							deliverMu.Unlock()
						}
					}
				},
				OnClose: handlers.OnClose,
				OnError: handlers.OnError,
			}

			baseTransport, err := baseFactory(wrappedHandlers)
			if err != nil {
				return nil, err
			}
			return &terrDelayTransport{
				base: baseTransport,
				inspectSend: func(chunk []byte) {
					msgs, pushErr := clientDec.Push(chunk)
					if pushErr != nil {
						return
					}
					for _, msg := range msgs {
						req, ok := msg.(*protocol.RequestEnvelope)
						if !ok || req == nil {
							continue
						}
						tgt, ok := req.Target.(*protocol.SessionTarget)
						if !ok || tgt == nil || tgt.SessionId != "demo-2" {
							continue
						}
						var sc chord.ServiceCall
						if err := jsonx.Decode(req.Call, &sc); err != nil {
							continue
						}
						if ctrl := chord.DecodeServiceControlCall(sc); ctrl != nil {
							if sub, ok := ctrl.(chord.ServiceControlCallSubscribe); ok && sub.ServiceId == services.ModelsService.Id {
								mu.Lock()
								delayedReqId = req.Id
								mu.Unlock()
							}
						}
					}
				},
			}, nil
		}

		c, err := client.Connect(client.ClientOptions{
			ServerId:         srv.ServerId,
			TransportFactory: wrappedFactory,
		})
		if err != nil {
			t.Fatalf("Connect: %v", err)
		}
		env.trackClient(c)

		var errsMu sync.Mutex
		var svcErrs []error
		binding := testkit.CreateSessionServiceBinding(c, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{{Id: services.ModelsService.Id}},
			OnError: func(err error) {
				errsMu.Lock()
				svcErrs = append(svcErrs, err)
				errsMu.Unlock()
			},
		})
		modelsSvc := services.AsModels(binding.Use(services.ModelsService))
		if err := binding.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("binding.Ready: %v", err)
		}

		var statesMu sync.Mutex
		var states []services.SessionAttachmentState
		unsub := binding.Attachment().Subscribe(func(st services.SessionAttachmentState, _ chord.Context, _ chord.ReplicatedStateDelivery) error {
			statesMu.Lock()
			states = append(states, st)
			statesMu.Unlock()
			return nil
		})

		terrAttachSession(t, c, "demo-2")
		if st, ok := binding.Attachment().Value(); !ok || st.Status != "attaching" || st.SessionId != "demo-2" {
			t.Fatalf("expected attaching/demo-2 while subscribe delayed, got %+v (%v)", st, ok)
		}

		terrAttachSession(t, c, "demo-1")
		if err := binding.WhenAttached("demo-1", chord.BackgroundContext); err != nil {
			t.Fatalf("WhenAttached(demo-1): %v", err)
		}
		if st, ok := binding.Attachment().Value(); !ok || st.Status != "attached" || st.SessionId != "demo-1" {
			t.Fatalf("attachment after supersede = %+v (%v)", st, ok)
		}
		ms, ok := modelsSvc.State().Value()
		if !ok || ms.Configuration.Model == nil || ms.Configuration.Model.Provider != terrDefaultProv || ms.Configuration.Model.ModelId != terrDefaultModel {
			t.Fatalf("models state = %+v (%v)", ms.Configuration.Model, ok)
		}

		releaseDelay()
		unsub()
		_ = binding.Dispose(chord.BackgroundContext)

		statesMu.Lock()
		copiedStates := append([]services.SessionAttachmentState(nil), states...)
		statesMu.Unlock()
		latestAttach := -1
		for i, st := range copiedStates {
			if st.Status == "attaching" && st.SessionId == "demo-1" {
				latestAttach = i
			}
		}
		if latestAttach < 0 {
			t.Fatalf("missing attaching/demo-1 in %+v", copiedStates)
		}
		for _, st := range copiedStates[latestAttach:] {
			if st.SessionId == "demo-2" && (st.Status == "attached" || st.Status == "degraded") {
				t.Fatalf("unexpected superseded demo-2 state after demo-1 attach: %+v", st)
			}
		}
		errsMu.Lock()
		gotErrs := append([]error(nil), svcErrs...)
		errsMu.Unlock()
		if len(gotErrs) != 0 {
			t.Fatalf("unexpected service errors: %v", gotErrs)
		}
	})

	t.Run("observes keyed service instances and fences replacement generations over framed transport", func(t *testing.T) {
		env := terrNewEnv(t)
		t.Setenv(terrFauxWorkerEnv, "1")
		_, srv := env.makeServer()
		c := env.attachClient(srv, "demo-1")

		var errsMu sync.Mutex
		var svcErrs []error
		source := services.CreateSessionServiceSource(c, services.ServiceSourceOptions{
			OnError: func(err error) {
				errsMu.Lock()
				svcErrs = append(svcErrs, err)
				errsMu.Unlock()
			},
		})
		cat, err := source.Catalogue(chord.BackgroundContext)
		if err != nil {
			t.Fatalf("Catalogue: %v", err)
		}
		if !slices.Contains(cat, chord.ServiceCatalogueEntry{ServiceId: terrKeyedProbeService.Id, Mode: chord.ServiceModeKeyed}) {
			t.Fatalf("Catalogue = %+v, missing %s keyed", cat, terrKeyedProbeService.Id)
		}

		type observedEntry struct {
			service any
			value   string
		}
		var obsMu sync.Mutex
		var observed []observedEntry
		consumer := chord.DefineFacet(chord.Facet{
			Id: "@test/keyed-probe-consumer",
			Setup: func(env chord.FacetEnvironment) error {
				env.Observe(terrKeyedProbeService, func(raw any, _ chord.Context) error {
					val, _ := terrReadProbeValue(t, raw)
					obsMu.Lock()
					observed = append(observed, observedEntry{service: raw, value: val})
					obsMu.Unlock()
					return nil
				})
				return nil
			},
		})
		host, err := chord.CreateFacetHost(&chord.FacetOptions{
			Facets:         []chord.Facet{consumer},
			ServiceSources: []chord.RemoteServiceSource{source},
		})
		if err != nil {
			t.Fatalf("CreateFacetHost: %v", err)
		}

		att, ok := source.Attachment().Value()
		if !ok || att.Status != "attached" || att.SessionId != "demo-1" {
			t.Fatalf("attachment = %+v (%v)", att, ok)
		}
		terrWaitFor(t, "observed length 1", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) >= 1
		})
		obsMu.Lock()
		firstEntry := observed[0]
		obsMu.Unlock()
		if firstEntry.value != "first" {
			t.Fatalf("observed[0].value = %q, want \"first\"", firstEntry.value)
		}

		if err := terrCallProbeReplace(firstEntry.service, "second", chord.BackgroundContext); err != nil {
			t.Fatalf("first replace: %v", err)
		}
		terrWaitFor(t, "observed length 2", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) >= 2
		})
		obsMu.Lock()
		secondEntry := observed[1]
		obsMu.Unlock()
		if secondEntry.value != "second" {
			t.Fatalf("observed[1].value = %q, want \"second\"", secondEntry.value)
		}
		if err := terrCallProbeReplace(firstEntry.service, "late", chord.BackgroundContext); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected 'observation is closed' from staleReplace, got %v", err)
		}

		terrAttachSession(t, c, "demo-2")
		if err := source.WhenAttached("demo-2", chord.BackgroundContext); err != nil {
			t.Fatalf("WhenAttached(demo-2): %v", err)
		}
		terrWaitFor(t, "observed length 3", func() bool {
			obsMu.Lock()
			defer obsMu.Unlock()
			return len(observed) >= 3
		})
		obsMu.Lock()
		thirdEntry := observed[2]
		obsMu.Unlock()
		if att2, _ := source.Attachment().Value(); att2.Status != "attached" || att2.SessionId != "demo-2" {
			t.Fatalf("attachment after demo-2 = %+v", att2)
		}
		if thirdEntry.value != "first" {
			t.Fatalf("observed[2].value = %q, want \"first\"", thirdEntry.value)
		}
		if err := terrCallProbeReplace(secondEntry.service, "late", chord.BackgroundContext); err == nil || !strings.Contains(err.Error(), "observation is closed") {
			t.Fatalf("expected 'observation is closed' from replacedReplace, got %v", err)
		}
		errsMu.Lock()
		gotErrs := append([]error(nil), svcErrs...)
		errsMu.Unlock()
		if len(gotErrs) != 0 {
			t.Fatalf("unexpected service errors: %v", gotErrs)
		}
		if err := host.Dispose(); err != nil {
			t.Fatalf("host.Dispose: %v", err)
		}
		if err := source.Dispose(chord.BackgroundContext); err != nil {
			t.Fatalf("source.Dispose: %v", err)
		}
	})

	t.Run("answers a client prompt through the worker-owned service provider", func(t *testing.T) {
		env := terrNewEnv(t)
		t.Setenv(terrFauxWorkerEnv, "1")
		dir, _ := env.makeServer()

		res, err := experimental.RunClient(experimentalcli.ClientCommand{
			Command:   "client",
			SessionId: terrPtr("demo-1"),
			Prompt:    terrPtr("question"),
		}, experimental.RunClientOptions{Directory: dir})
		if err != nil {
			t.Fatalf("RunClient(prompt): %v", err)
		}
		if res.Kind != experimental.ClientResultKindPrompted || res.Text != "deterministic remote answer" {
			t.Fatalf("RunClient = %+v, want prompted with deterministic remote answer", res)
		}
	})

	t.Run("replicates the transcript after consecutive prompts", func(t *testing.T) {
		env := terrNewEnv(t)
		t.Setenv(terrFauxWorkerEnv, "1")
		_, srv := env.makeServer()
		c := env.attachClient(srv, "demo-1")

		binding := testkit.CreateSessionServiceBinding(c, testkit.ExperimentalServiceBindingOptions{
			Services: []chord.ServiceRef{
				{Id: services.AgentControllerService.Id},
				{Id: services.SessionPluginsService.Id},
				{Id: services.TranscriptService.Id},
			},
		})
		defer func() { _ = binding.Dispose(chord.BackgroundContext) }()
		ctrl := services.AsAgentController(binding.Use(services.AgentControllerService))
		transcript := services.AsTranscript(binding.Use(services.TranscriptService))
		if err := binding.Ready(chord.BackgroundContext); err != nil {
			t.Fatalf("binding.Ready: %v", err)
		}
		if err := services.AsSessionPlugins(binding.Use(services.SessionPluginsService)).Reload(chord.BackgroundContext); err != nil {
			t.Fatalf("SessionPlugins.Reload: %v", err)
		}

		for idx, msg := range []string{"first question", "second question"} {
			resp, err := ctrl.Prompt(services.AgentPromptRequest{Message: msg}, chord.BackgroundContext)
			if err != nil {
				t.Fatalf("Prompt(%q): %v", msg, err)
			}
			if !resp.Accepted || resp.OperationId == nil {
				t.Fatalf("Prompt(%q) not accepted: %+v", msg, resp)
			}
			res, err := ctrl.WaitForPrompt(*resp.OperationId, chord.BackgroundContext)
			if err != nil {
				t.Fatalf("WaitForPrompt(%q): %v", msg, err)
			}
			if res.Status != "done" || res.Text == nil || *res.Text != "deterministic remote answer" || res.Reason != nil {
				t.Fatalf("WaitForPrompt(%q) = %+v", msg, res)
			}
			wantCount := idx + 1
			terrWaitFor(t, fmt.Sprintf("assistant count == %d", wantCount), func() bool {
				view, ok := transcript.State().Value()
				if !ok {
					return false
				}
				count := 0
				for _, entry := range view.Entries {
					if entry.Kind == "pi.assistant" {
						count++
					}
				}
				return count == wantCount
			})
		}
	})

	t.Run("stops an idle Session worker after its client disconnects", func(t *testing.T) {
		env := terrNewEnv(t)
		_, srv := env.makeServer()
		c := env.attachClient(srv, "demo-1")
		pid, ok := srv.WorkerPids()["demo-1"]
		if !ok || pid <= 0 {
			t.Fatalf("expected worker pid, got %v", srv.WorkerPids())
		}

		_ = c.Dispose()
		env.untrackClient(c)
		terrWaitFor(t, "workerPid removed from server", func() bool {
			_, has := srv.WorkerPids()["demo-1"]
			return !has
		})
		terrWaitFor(t, "worker process exited", func() bool { return !terrProcessExists(pid) })
	})

	t.Run("starts one process per attached session and stops them during shutdown", func(t *testing.T) {
		env := terrNewEnv(t)
		_, srv := env.makeServer()
		_ = env.attachClient(srv, "demo-1")
		_ = env.attachClient(srv, "demo-2")

		pids := srv.WorkerPids()
		if len(pids) != 2 || pids["demo-1"] == pids["demo-2"] {
			t.Fatalf("WorkerPids = %+v, want 2 distinct pids", pids)
		}
		for _, pid := range pids {
			if !terrProcessExists(pid) {
				t.Fatalf("expected worker pid %d to be alive", pid)
			}
		}

		if err := srv.Close(); err != nil {
			t.Fatalf("srv.Close: %v", err)
		}
		if len(srv.WorkerPids()) != 0 {
			t.Fatalf("expected empty WorkerPids after Close, got %+v", srv.WorkerPids())
		}
		for _, pid := range pids {
			p := pid
			terrWaitFor(t, fmt.Sprintf("worker %d exited", p), func() bool { return !terrProcessExists(p) })
		}
	})

	t.Run("server runtime replaces an exited worker on the next attach", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "pew-")
		srv, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer: %v", err)
		}
		env.trackServer(srv)
		c := env.attachClient(srv, "demo-1")
		firstPid := srv.WorkerPids()["demo-1"]
		if firstPid <= 0 {
			t.Fatalf("expected firstPid > 0, got %d", firstPid)
		}
		if err := syscall.Kill(firstPid, syscall.SIGKILL); err != nil {
			t.Fatalf("Kill(%d): %v", firstPid, err)
		}
		terrWaitFor(t, "killed worker removed", func() bool {
			_, has := srv.WorkerPids()["demo-1"]
			return !has
		})
		terrAttachSession(t, c, "demo-1")
		replacementPid := srv.WorkerPids()["demo-1"]
		if replacementPid <= 0 || replacementPid == firstPid {
			t.Fatalf("replacementPid = %d, firstPid = %d", replacementPid, firstPid)
		}
	})

	t.Run("discovers workers after replacing the server", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "per-")
		first, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(first): %v", err)
		}
		env.trackServer(first)
		_ = env.attachClient(first, "demo-1")
		firstWorkerPid := first.WorkerPids()["demo-1"]
		if firstWorkerPid <= 0 {
			t.Fatalf("expected firstWorkerPid > 0")
		}

		replacement, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(replacement): %v", err)
		}
		env.trackServer(replacement)
		<-first.Closed()
		terrWaitFor(t, "first.WorkerPids empty", func() bool { return len(first.WorkerPids()) == 0 })
		if !terrProcessExists(firstWorkerPid) {
			t.Fatalf("expected worker %d to stay alive across server replacement", firstWorkerPid)
		}

		listRes, err := experimental.RunClient(experimentalcli.ClientCommand{Command: "client"}, experimental.RunClientOptions{Directory: dir})
		if err != nil {
			t.Fatalf("RunClient(list): %v", err)
		}
		want := []services.SessionAddress{
			{ServerId: first.ServerId, SessionId: "demo-1"},
			{ServerId: first.ServerId, SessionId: "demo-2"},
		}
		if listRes.Kind != experimental.ClientResultKindList || !slices.Equal(listRes.Sessions, want) {
			t.Fatalf("list = %+v, want %+v", listRes.Sessions, want)
		}

		_ = env.attachClient(replacement, "demo-1")
		if got := replacement.WorkerPids()["demo-1"]; got != firstWorkerPid {
			t.Fatalf("replacement WorkerPids[demo-1] = %d, want %d", got, firstWorkerPid)
		}
		_ = env.attachClient(replacement, "demo-2")
		p2 := replacement.WorkerPids()["demo-2"]
		if p2 <= 0 || p2 == firstWorkerPid {
			t.Fatalf("replacement WorkerPids[demo-2] = %d", p2)
		}
	})

	t.Run("retires an unclaimed idle worker after replacement demand expires", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "pow-")
		t.Setenv(experimental.SessionWorkerOrphanDemandGraceEnv, "50")

		first, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(first): %v", err)
		}
		env.trackServer(first)
		_ = env.attachClient(first, "demo-1")
		workerPid := first.WorkerPids()["demo-1"]
		if workerPid <= 0 {
			t.Fatalf("expected workerPid > 0")
		}

		replacement, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(replacement): %v", err)
		}
		env.trackServer(replacement)
		<-first.Closed()
		if got := replacement.WorkerPids()["demo-1"]; got != workerPid {
			t.Fatalf("replacement WorkerPids[demo-1] = %d, want %d", got, workerPid)
		}

		terrWaitFor(t, "unclaimed idle worker retired", func() bool {
			_, has := replacement.WorkerPids()["demo-1"]
			return !has
		})
		terrWaitFor(t, "unclaimed worker process exited", func() bool { return !terrProcessExists(workerPid) })
	})

	t.Run("restores tracked sessions that are outside the replacement catalog", func(t *testing.T) {
		env := terrNewEnv(t)
		dir := terrMkdirTemp(t, "pet-")
		emptySessionDir := terrMkdirTemp(t, "pets-")

		first, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: dir,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(first): %v", err)
		}
		env.trackServer(first)
		_ = env.attachClient(first, "demo-1")
		workerPid := first.WorkerPids()["demo-1"]

		replacement, err := experimental.StartServer(experimental.StartServerOptions{
			Directory:  dir,
			Provider:   terrDefaultProv,
			Model:      terrDefaultModel,
			SessionDir: emptySessionDir,
		})
		if err != nil {
			t.Fatalf("StartServer(replacement): %v", err)
		}
		env.trackServer(replacement)
		<-first.Closed()

		listRes, err := experimental.RunClient(experimentalcli.ClientCommand{Command: "client"}, experimental.RunClientOptions{Directory: dir})
		if err != nil {
			t.Fatalf("RunClient(list): %v", err)
		}
		want := []services.SessionAddress{{ServerId: first.ServerId, SessionId: "demo-1"}}
		if listRes.Kind != experimental.ClientResultKindList || !slices.Equal(listRes.Sessions, want) {
			t.Fatalf("list = %+v, want %+v", listRes.Sessions, want)
		}

		_ = env.attachClient(replacement, "demo-1")
		if got := replacement.WorkerPids()["demo-1"]; got != workerPid {
			t.Fatalf("replacement WorkerPids[demo-1] = %d, want %d", got, workerPid)
		}
	})

	t.Run("reports missing and ambiguous session selections", func(t *testing.T) {
		env := terrNewEnv(t)
		sharedDir := terrMkdirTemp(t, "ped-")
		id1 := protocol.ServerId("00000000-0000-4000-8000-000000000001")
		id2 := protocol.ServerId("00000000-0000-4000-8000-000000000002")

		s1, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: sharedDir,
			ServerId:  &id1,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(1): %v", err)
		}
		env.trackServer(s1)
		s2, err := experimental.StartServer(experimental.StartServerOptions{
			Directory: sharedDir,
			ServerId:  &id2,
			Provider:  terrDefaultProv,
			Model:     terrDefaultModel,
		})
		if err != nil {
			t.Fatalf("StartServer(2): %v", err)
		}
		env.trackServer(s2)

		_, err = experimental.RunClient(
			experimentalcli.ClientCommand{Command: "client", SessionId: terrPtr("missing")},
			experimental.RunClientOptions{Directory: sharedDir},
		)
		if err == nil || !strings.Contains(err.Error(), "No discovered server contains session missing") {
			t.Fatalf("expected missing session error, got %v", err)
		}

		_, err = experimental.RunClient(
			experimentalcli.ClientCommand{Command: "client", SessionId: terrPtr("demo-1")},
			experimental.RunClientOptions{Directory: sharedDir},
		)
		if err == nil || !strings.Contains(err.Error(), "Session demo-1 is available from more than one server") {
			t.Fatalf("expected ambiguous session error, got %v", err)
		}
	})

	t.Run("rejects a duplicate session ID within one session directory", func(t *testing.T) {
		env := terrNewEnv(t)
		_, err := testkit.CreateExperimentalSessions(
			filepath.Join(env.agentDir, "experimental", "sessions"),
			[]string{"demo-1"},
			filepath.Join(env.agentDir, "other-cwd"),
		)
		if err == nil || !strings.Contains(err.Error(), "Session demo-1 already exists") {
			t.Fatalf("expected duplicate session error, got %v", err)
		}
	})
}

type terrDelayTransport struct {
	base        client.ByteTransport
	inspectSend func(chunk []byte)
}

func (d *terrDelayTransport) Send(chunk []byte) error {
	if d.inspectSend != nil {
		d.inspectSend(chunk)
	}
	return d.base.Send(chunk)
}

func (d *terrDelayTransport) Close() {
	d.base.Close()
}
