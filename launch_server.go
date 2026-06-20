package agentd

import (
	"context"
	"os"
	"os/signal"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	"google.golang.org/adk/cmd/launcher"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/cmd/launcher/prod"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
)

// serverConfig holds the resolved settings for the HTTP launchers. The ADK
// service types live here as unexported fields only; they never appear in a
// public agentd signature.
type serverConfig struct {
	ctx         context.Context
	subAgents   []agent.Agent
	sessionSvc  session.Service
	artifactSvc artifact.Service
	memorySvc   memory.Service
	args        []string
}

// ServerOption configures the HTTP launchers [LaunchAPI] and [LaunchWeb].
//
// Service selection is expressed with concise, parameterless named options
// (for example [WithInMemoryArtifactService]); callers never name or construct
// an ADK service type. Session and artifact storage default to in-memory, so
// the zero-config calls LaunchAPI(root) / LaunchWeb(root) just work.
type ServerOption func(*serverConfig) error

// WithSubAgents registers additional agents reachable by the server. With at
// least one sub-agent the root and sub-agents are exposed via a multi-agent
// loader; otherwise a single-agent loader is used.
func WithSubAgents(agents ...agent.Agent) ServerOption {
	return func(c *serverConfig) error {
		c.subAgents = append(c.subAgents, agents...)
		return nil
	}
}

// WithInMemorySessionService uses an ephemeral, process-local session store.
// This is also the default, so it is only needed to be explicit.
func WithInMemorySessionService() ServerOption {
	return func(c *serverConfig) error {
		c.sessionSvc = session.InMemoryService()
		return nil
	}
}

// WithInMemoryArtifactService uses an ephemeral, process-local artifact store.
// This is also the default, so it is only needed to be explicit.
func WithInMemoryArtifactService() ServerOption {
	return func(c *serverConfig) error {
		c.artifactSvc = artifact.InMemoryService()
		return nil
	}
}

// WithInMemoryMemoryService enables an ephemeral, process-local long-term
// memory store. Long-term memory is opt-in (off by default).
func WithInMemoryMemoryService() ServerOption {
	return func(c *serverConfig) error {
		c.memorySvc = memory.InMemoryService()
		return nil
	}
}

// WithServerArgs overrides the command-line arguments passed to the underlying
// ADK launcher, replacing the defaults entirely. The arguments must include the
// sublauncher keywords, e.g. WithServerArgs("-port=9090", "api") for the API
// server or WithServerArgs("web", "-port=9090", "webui", "api") for the web UI.
func WithServerArgs(args ...string) ServerOption {
	return func(c *serverConfig) error {
		c.args = args
		return nil
	}
}

// WithServerContext supplies a context controlling the server lifecycle. When
// omitted, the launcher installs a context that is cancelled on SIGINT
// (Ctrl+C) for graceful shutdown.
func WithServerContext(ctx context.Context) ServerOption {
	return func(c *serverConfig) error {
		c.ctx = ctx
		return nil
	}
}

// newServerConfig applies the default options (session + artifact in-memory)
// first so any caller-supplied option that follows overrides them.
func newServerConfig(opts ...ServerOption) (*serverConfig, error) {
	all := append([]ServerOption{
		WithInMemorySessionService(),
		WithInMemoryArtifactService(),
	}, opts...)

	cfg := &serverConfig{}
	for _, o := range all {
		if err := o(cfg); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// resolveServer builds the agent loader, launcher.Config, and final args from
// the options. It has no context or signal side effects, so it is unit-testable.
func resolveServer(defaultArgs []string, root agent.Agent, opts ...ServerOption) (*launcher.Config, []string, error) {
	cfg, err := newServerConfig(opts...)
	if err != nil {
		return nil, nil, err
	}

	var loader agent.Loader = agent.NewSingleLoader(root)
	if len(cfg.subAgents) > 0 {
		ml, err := agent.NewMultiLoader(root, cfg.subAgents...)
		if err != nil {
			return nil, nil, err
		}
		loader = ml
	}

	args := defaultArgs
	if cfg.args != nil {
		args = cfg.args
	}

	return &launcher.Config{
		SessionService:  cfg.sessionSvc,
		ArtifactService: cfg.artifactSvc,
		MemoryService:   cfg.memorySvc,
		AgentLoader:     loader,
	}, args, nil
}

// runServer is the shared engine behind LaunchAPI and LaunchWeb. It resolves the
// launcher config, installs a SIGINT-cancelled context unless one was supplied,
// and blocks in the launcher until shutdown.
func runServer(l launcher.Launcher, defaultArgs []string, root agent.Agent, opts ...ServerOption) error {
	cfg, err := newServerConfig(opts...)
	if err != nil {
		return err
	}

	lcfg, args, err := resolveServer(defaultArgs, root, opts...)
	if err != nil {
		return err
	}

	ctx := cfg.ctx
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = signal.NotifyContext(context.Background(), os.Interrupt)
		defer cancel()
	}
	return l.Execute(ctx, lcfg, args)
}

// LaunchAPI serves the ADK REST API (and A2A) for the agent on :8080 under the
// /api path prefix. It blocks until the process receives SIGINT (Ctrl+C) or the
// context supplied via [WithServerContext] is cancelled.
//
// The simplest form needs no options:
//
//	if err := agentd.LaunchAPI(root); err != nil {
//		log.Fatal(err)
//	}
//
// Use [WithServerArgs] to change the port or path prefix, e.g.
// agentd.LaunchAPI(root, agentd.WithServerArgs("-port=9090", "api")).
func LaunchAPI(root agent.Agent, opts ...ServerOption) error {
	return runServer(prod.NewLauncher(), []string{"api"}, root, opts...)
}

// LaunchWeb serves the ADK Web UI together with the REST API on :8080 (UI at
// /ui, API at /api). It blocks until SIGINT (Ctrl+C) or the context supplied
// via [WithServerContext] is cancelled. Use it for local development and demos.
//
//	if err := agentd.LaunchWeb(root); err != nil {
//		log.Fatal(err)
//	}
func LaunchWeb(root agent.Agent, opts ...ServerOption) error {
	return runServer(full.NewLauncher(), []string{"web", "webui", "api"}, root, opts...)
}
