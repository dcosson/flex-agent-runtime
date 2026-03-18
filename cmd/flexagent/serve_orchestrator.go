package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awsec2 "github.com/aws/aws-sdk-go-v2/service/ec2"
	awsssm "github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/dcosson/flex-agent-runtime/internal/agent"
	agentapi "github.com/dcosson/flex-agent-runtime/internal/agent/api"
	"github.com/dcosson/flex-agent-runtime/internal/orchestrator"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/api"
	rpcclient "github.com/dcosson/flex-agent-runtime/internal/rpc/client"
	"github.com/dcosson/flex-agent-runtime/internal/rpc/transport"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	directcontrol "github.com/dcosson/flex-agent-runtime/internal/sandbox/control/direct"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/fleet"
	ec2provisioner "github.com/dcosson/flex-agent-runtime/internal/sandbox/control/fleet/ec2"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
	nodecontrol "github.com/dcosson/flex-agent-runtime/internal/sandbox/control/node"
)

type serveOrchestratorConfig struct {
	ListenAddr string

	SandboxHostAddr string

	DirectHostAddr            string
	DirectAMIID               string
	DirectSubnetID            string
	DirectSecurityGroupIDsRaw string
	DirectInstanceProfileARN  string
	DirectInstanceType        string

	MaxSessions      int
	AgentMaxSessions int

	ShutdownTimeout      time.Duration
	HealthCheckInterval  time.Duration
	CreateSessionTimeout time.Duration

	AuthToken string

	RPCMaxMessageBytes int
	APIVersion         string
	MinAPIVersion      string
}

func parseServeOrchestratorConfig(args []string) serveOrchestratorConfig {
	var cfg serveOrchestratorConfig
	fs := flag.NewFlagSet("flexagent serve orchestrator", flag.ExitOnError)

	fs.StringVar(&cfg.ListenAddr, "listen", envOrDefault("FLEXAGENT_LISTEN", ":8080"), "listen address")

	fs.StringVar(&cfg.SandboxHostAddr, "sandbox-host-addr", envOrDefault("ORCHESTRATOR_SANDBOX_HOST_ADDR", ""), "comma-separated sandbox-host RPC addresses")

	fs.StringVar(&cfg.DirectHostAddr, "direct-host-addr", envOrDefault("ORCHESTRATOR_DIRECT_HOST_ADDR", ""), "pre-existing host:port for agent-direct mode (skips EC2 provisioning)")
	fs.StringVar(&cfg.DirectAMIID, "direct-ami-id", envOrDefault("ORCHESTRATOR_DIRECT_AMI_ID", ""), "AMI ID for direct mode")
	fs.StringVar(&cfg.DirectSubnetID, "direct-subnet-id", envOrDefault("ORCHESTRATOR_DIRECT_SUBNET_ID", ""), "subnet ID for direct mode")
	fs.StringVar(&cfg.DirectSecurityGroupIDsRaw, "direct-security-group-ids", envOrDefault("ORCHESTRATOR_DIRECT_SG_IDS", ""), "comma-separated security group IDs")
	fs.StringVar(&cfg.DirectInstanceProfileARN, "direct-instance-profile-arn", envOrDefault("ORCHESTRATOR_DIRECT_INSTANCE_PROFILE", ""), "IAM instance profile ARN for direct mode")
	fs.StringVar(&cfg.DirectInstanceType, "direct-instance-type", envOrDefault("ORCHESTRATOR_DIRECT_INSTANCE_TYPE", "t3.medium"), "default EC2 instance type for direct mode")

	fs.IntVar(&cfg.MaxSessions, "max-sessions", envIntOrDefault("ORCHESTRATOR_MAX_SESSIONS", 0), "maximum concurrent orchestrator sessions (0 = unlimited)")
	fs.IntVar(&cfg.AgentMaxSessions, "agent-max-sessions", envIntOrDefault("ORCHESTRATOR_AGENT_MAX_SESSIONS", 0), "max in-process agent sessions (future tools-sandbox mode)")

	fs.DurationVar(&cfg.ShutdownTimeout, "shutdown-timeout", envDurationOrDefault("FLEXAGENT_SHUTDOWN_TIMEOUT", 30*time.Second), "graceful shutdown timeout")
	fs.DurationVar(&cfg.HealthCheckInterval, "health-check-interval", envDurationOrDefault("ORCHESTRATOR_HEALTH_INTERVAL", 15*time.Second), "health check interval")
	fs.DurationVar(&cfg.CreateSessionTimeout, "create-session-timeout", envDurationOrDefault("ORCHESTRATOR_CREATE_TIMEOUT", 3*time.Minute), "create session timeout")

	fs.StringVar(&cfg.AuthToken, "auth-token", envOrDefault("FLEXAGENT_AUTH_TOKEN", ""), "bearer auth token")

	fs.IntVar(&cfg.RPCMaxMessageBytes, "rpc-max-message-bytes", envIntOrDefault("FLEXAGENT_RPC_MAX_MESSAGE_BYTES", 16<<20), "max rpc message size in bytes")
	fs.StringVar(&cfg.APIVersion, "api-version", envOrDefault("FLEXAGENT_API_VERSION", "v1"), "advertised api version")
	fs.StringVar(&cfg.MinAPIVersion, "min-api-version", envOrDefault("FLEXAGENT_MIN_API_VERSION", "v1"), "minimum supported api version")

	_ = fs.Parse(args)
	return cfg
}

func (cfg serveOrchestratorConfig) validate() error {
	var errs []error
	if strings.TrimSpace(cfg.ListenAddr) == "" {
		errs = append(errs, fmt.Errorf("listen is required"))
	}
	if cfg.MaxSessions < 0 {
		errs = append(errs, fmt.Errorf("max-sessions must be >= 0"))
	}
	if cfg.AgentMaxSessions < 0 {
		errs = append(errs, fmt.Errorf("agent-max-sessions must be >= 0"))
	}
	if cfg.RPCMaxMessageBytes <= 0 {
		errs = append(errs, fmt.Errorf("rpc-max-message-bytes must be > 0"))
	}
	if strings.TrimSpace(cfg.APIVersion) == "" {
		errs = append(errs, fmt.Errorf("api-version is required"))
	}
	if strings.TrimSpace(cfg.MinAPIVersion) == "" {
		errs = append(errs, fmt.Errorf("min-api-version is required"))
	}
	if cfg.CreateSessionTimeout <= 0 {
		errs = append(errs, fmt.Errorf("create-session-timeout must be > 0"))
	}
	if cfg.ShutdownTimeout <= 0 {
		errs = append(errs, fmt.Errorf("shutdown-timeout must be > 0"))
	}
	if cfg.HealthCheckInterval <= 0 {
		errs = append(errs, fmt.Errorf("health-check-interval must be > 0"))
	}

	hasDirectHost := strings.TrimSpace(cfg.DirectHostAddr) != ""
	hasDirectEC2 := strings.TrimSpace(cfg.DirectAMIID) != ""
	hasSandboxHost := strings.TrimSpace(cfg.SandboxHostAddr) != ""
	if !hasDirectHost && !hasDirectEC2 && !hasSandboxHost {
		errs = append(errs, fmt.Errorf("at least one backend must be configured: direct-host-addr, direct-ami-id, or sandbox-host"))
	}
	if hasDirectHost && hasDirectEC2 {
		errs = append(errs, fmt.Errorf("direct-host-addr and direct-ami-id are mutually exclusive"))
	}
	if hasDirectEC2 {
		if strings.TrimSpace(cfg.DirectSubnetID) == "" {
			errs = append(errs, fmt.Errorf("direct-subnet-id is required when direct-ami-id is set"))
		}
		if len(cfg.directSecurityGroupIDs()) == 0 {
			errs = append(errs, fmt.Errorf("direct-security-group-ids is required when direct-ami-id is set"))
		}
	}
	return errors.Join(errs...)
}

func (cfg serveOrchestratorConfig) directSecurityGroupIDs() []string {
	return splitCommaList(cfg.DirectSecurityGroupIDsRaw)
}

func runServeOrchestrator(args []string) {
	cfg := parseServeOrchestratorConfig(args)
	logger := slog.Default()
	if err := cfg.validate(); err != nil {
		logger.Error("invalid configuration", "error", err)
		os.Exit(1)
	}

	// Build DirectSandboxControl if direct mode is configured.
	var directControl control.SandboxControl
	if strings.TrimSpace(cfg.DirectHostAddr) != "" {
		directControl = newStaticHostControl(cfg.DirectHostAddr)
		logger.Info("using static direct host", "addr", cfg.DirectHostAddr)
	} else if strings.TrimSpace(cfg.DirectAMIID) != "" {
		dc, dcErr := buildDirectControl(context.Background(), cfg, logger)
		if dcErr != nil {
			logger.Error("failed to build direct sandbox control", "error", dcErr)
			os.Exit(1)
		}
		directControl = dc
	}

	// Build Node/Fleet SandboxControl if sandbox-host addresses are configured.
	var nodeControl control.SandboxControl
	var sandboxHostAddrs []string
	if strings.TrimSpace(cfg.SandboxHostAddr) != "" {
		sandboxHostAddrs = splitCommaList(cfg.SandboxHostAddr)
		nc, ncErr := buildNodeOrFleetControl(sandboxHostAddrs, cfg, logger)
		if ncErr != nil {
			logger.Error("failed to build sandbox-host control", "error", ncErr)
			os.Exit(1)
		}
		nodeControl = nc
	}

	// Build in-process AgentLoopService for tools-sandbox mode.
	var agentLoopSvc agentapi.AgentService
	if nodeControl != nil {
		agentLoopSvc = agent.NewAgentLoopService(
			nil,
			agent.WithMaxSessions(cfg.AgentMaxSessions),
			agent.WithCloseDrainTimeout(cfg.ShutdownTimeout),
			agent.WithToolCatalogFactory(newRuntimeToolCatalogFactory(logger)),
		)
	}

	agentLaunchArgs := []string{"serve", "agent", "--listen", ":8081", "--api-version", cfg.APIVersion, "--min-api-version", cfg.MinAPIVersion}

	agentEnv := map[string]string{}
	if cfg.AuthToken != "" {
		agentEnv["FLEXAGENT_AUTH_TOKEN"] = cfg.AuthToken
	}

	orch, err := orchestrator.New(orchestrator.OrchestratorConfig{
		DirectControl:    directControl,
		NodeControl:      nodeControl,
		AgentLoopService: agentLoopSvc,
		SandboxHostAddrs: sandboxHostAddrs,
		MaxSessions:      cfg.MaxSessions,
		HealthInterval:   cfg.HealthCheckInterval,
		CreateTimeout:    cfg.CreateSessionTimeout,
		ShutdownTimeout:  cfg.ShutdownTimeout,
		Logger:           logger,
		AgentBinary:      "flexagent",
		AgentPort:        8081,
		AgentArgs:        agentLaunchArgs,
		AgentEnv:         agentEnv,
		AgentServiceFactory: func(address string) (agentapi.AgentService, error) {
			clientCfg := transport.ClientConfig{
				APIVersion:      cfg.APIVersion,
				MaxMessageBytes: cfg.RPCMaxMessageBytes,
			}
			if cfg.AuthToken != "" {
				clientCfg.HeaderInjector = transport.HeaderTokenAuth("authorization", "Bearer "+cfg.AuthToken)
			}
			return rpcclient.NewAgentServiceClient(http.DefaultClient, control.AddressToURL(address), clientCfg), nil
		},
	})
	if err != nil {
		logger.Error("failed to initialize orchestrator", "error", err)
		os.Exit(1)
	}

	authHook := makeAuthHook(cfg.AuthToken)
	rpcTransport := transport.NewServer(
		transport.ServerConfig{
			MaxMessageBytes: cfg.RPCMaxMessageBytes,
			APIVersion:      cfg.APIVersion,
			MinAPIVersion:   cfg.MinAPIVersion,
			AuthHook:        authHook,
		},
		transport.WithAgentService(orch),
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", rpcTransport.Handler())

	httpServer := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("flexagent serve orchestrator starting", "listen", cfg.ListenAddr, "api_version", cfg.APIVersion)
	go func() {
		if serveErr := httpServer.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("orchestrator listen failed", "error", serveErr)
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	logger.Info("shutdown signal received, draining orchestrator", "timeout", cfg.ShutdownTimeout)

	if err := orch.Close(); err != nil {
		logger.Error("orchestrator close error", "error", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown error", "error", err)
	}

	logger.Info("flexagent serve orchestrator shutdown complete")
}

func buildDirectControl(ctx context.Context, cfg serveOrchestratorConfig, logger *slog.Logger) (control.SandboxControl, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	ec2Client := awsec2.NewFromConfig(awsCfg)
	ssmClient := awsssm.NewFromConfig(awsCfg)
	prov := ec2provisioner.NewEC2InstanceProvisionerWithLogger(ec2Client, ec2provisioner.EC2LaunchConfig{
		SubnetID:            cfg.DirectSubnetID,
		SecurityGroupIDs:    cfg.directSecurityGroupIDs(),
		InstanceProfileName: instanceProfileName(cfg.DirectInstanceProfileARN),
	}, logger)

	return directcontrol.NewDirectSandboxControl(
		prov,
		ssmClient,
		directcontrol.Config{
			AMIID:               cfg.DirectAMIID,
			DefaultInstanceType: cfg.DirectInstanceType,
			SubnetID:            cfg.DirectSubnetID,
			SecurityGroupIDs:    cfg.directSecurityGroupIDs(),
			InstanceProfileARN:  cfg.DirectInstanceProfileARN,
		},
		directcontrol.WithLogger(logger),
	)
}

func splitCommaList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		value := strings.TrimSpace(part)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

// buildNodeOrFleetControl constructs Node or Fleet SandboxControl based on
// the number of sandbox-host addresses.
// - 1 host  -> NodeSandboxControl
// - N hosts -> FleetSandboxControl
func buildNodeOrFleetControl(addrs []string, cfg serveOrchestratorConfig, logger *slog.Logger) (control.SandboxControl, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no sandbox-host addresses provided")
	}
	if len(addrs) == 1 {
		baseURL := addrs[0]
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			baseURL = control.AddressToURL(baseURL)
		}
		clientCfg := transport.ClientConfig{APIVersion: cfg.APIVersion}
		if cfg.AuthToken != "" {
			clientCfg.HeaderInjector = transport.HeaderTokenAuth("authorization", "Bearer "+cfg.AuthToken)
		}
		sandboxSvc := rpcclient.NewSandboxServiceClient(
			&http.Client{Timeout: 30 * time.Second},
			baseURL,
			clientCfg,
		)
		return nodecontrol.NewNodeSandboxControl(sandboxSvc, nodecontrol.WithLogger(logger)), nil
	}

	parsed := make([]parsedSandboxHostAddr, 0, len(addrs))
	for _, raw := range addrs {
		entry, err := parseSandboxHostAddr(raw)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, entry)
	}
	port := parsed[0].port
	for _, entry := range parsed[1:] {
		if entry.port != port {
			return nil, fmt.Errorf("fleet mode requires sandbox-host addresses to use the same port; got %d and %d", port, entry.port)
		}
	}

	provisioner := newStaticFleetProvisioner(parsed)
	clientFactory := func(addr string) (fleet.FleetNodeClient, error) {
		baseURL := addr
		if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
			baseURL = control.AddressToURL(baseURL)
		}
		clientCfg := transport.ClientConfig{APIVersion: cfg.APIVersion}
		if cfg.AuthToken != "" {
			clientCfg.HeaderInjector = transport.HeaderTokenAuth("authorization", "Bearer "+cfg.AuthToken)
		}
		sandboxSvc := rpcclient.NewSandboxServiceClient(
			&http.Client{Timeout: 30 * time.Second},
			baseURL,
			clientCfg,
		)
		nodeSvc := nodecontrol.NewNodeSandboxControl(sandboxSvc, nodecontrol.WithLogger(logger))
		return &fleetNodeClientAdapter{
			SandboxControl: nodeSvc,
			sandboxService: sandboxSvc,
		}, nil
	}

	fleetCfg := fleet.DefaultFleetConfig()
	fleetCfg.MinInstances = 0
	fleetCfg.MaxInstances = len(parsed)
	fleetCfg.WarmPoolTarget = len(parsed)
	fleetCfg.SandboxHostPort = port
	fleetCfg.HealthCheckInterval = cfg.HealthCheckInterval
	if cfg.MaxSessions > 0 {
		fleetCfg.MaxSessionsPerInstance = cfg.MaxSessions
	} else {
		fleetCfg.MaxSessionsPerInstance = math.MaxInt32
	}
	fleetCfg.InstanceConfig = instance.InstanceConfig{
		Tags: map[string]string{
			"ManagedBy": "flex-agent-runtime",
			"adapter":   "fleet-static",
		},
	}

	fleetControl, err := fleet.NewFleetSandboxControl(
		provisioner,
		clientFactory,
		fleetCfg,
		fleet.WithLogger(logger),
		fleet.WithLeaveInstancesOnClose(true),
	)
	if err != nil {
		return nil, err
	}
	return fleetControl, nil
}

func instanceProfileName(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 && idx+1 < len(trimmed) {
		return trimmed[idx+1:]
	}
	return trimmed
}

type parsedSandboxHostAddr struct {
	host string
	port int
}

var _ instance.InstanceProvisioner = (*staticFleetProvisioner)(nil)
var _ fleet.FleetNodeClient = (*fleetNodeClientAdapter)(nil)

func parseSandboxHostAddr(raw string) (parsedSandboxHostAddr, error) {
	addr := strings.TrimSpace(raw)
	if addr == "" {
		return parsedSandboxHostAddr{}, fmt.Errorf("sandbox-host address must not be empty")
	}
	hostPort := addr
	if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
		parsedURL, err := url.Parse(addr)
		if err != nil {
			return parsedSandboxHostAddr{}, fmt.Errorf("invalid sandbox-host address %q: %w", raw, err)
		}
		if parsedURL.Host == "" {
			return parsedSandboxHostAddr{}, fmt.Errorf("invalid sandbox-host address %q: missing host", raw)
		}
		if parsedURL.Path != "" && parsedURL.Path != "/" {
			return parsedSandboxHostAddr{}, fmt.Errorf("invalid sandbox-host address %q: URL path is not allowed", raw)
		}
		hostPort = parsedURL.Host
	}
	host, portRaw, err := net.SplitHostPort(hostPort)
	if err != nil {
		return parsedSandboxHostAddr{}, fmt.Errorf("sandbox-host address %q must be host:port", raw)
	}
	port, err := strconv.Atoi(portRaw)
	if err != nil || port <= 0 || port > 65535 {
		return parsedSandboxHostAddr{}, fmt.Errorf("sandbox-host address %q has invalid port %q", raw, portRaw)
	}
	if strings.TrimSpace(host) == "" {
		return parsedSandboxHostAddr{}, fmt.Errorf("sandbox-host address %q has empty host", raw)
	}
	return parsedSandboxHostAddr{host: host, port: port}, nil
}

type staticFleetProvisioner struct {
	mu         sync.Mutex
	instances  []instance.InstanceInfo
	nextLaunch int
}

func newStaticFleetProvisioner(hosts []parsedSandboxHostAddr) *staticFleetProvisioner {
	instances := make([]instance.InstanceInfo, 0, len(hosts))
	for i, host := range hosts {
		instances = append(instances, instance.InstanceInfo{
			InstanceID: fmt.Sprintf("static-%03d", i+1),
			PrivateIP:  host.host,
			State:      instance.CloudInstanceRunning,
		})
	}
	return &staticFleetProvisioner{instances: instances}
}

func (p *staticFleetProvisioner) LaunchInstance(_ context.Context, _ instance.InstanceConfig) (*instance.InstanceInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.nextLaunch >= len(p.instances) {
		return nil, fmt.Errorf("no additional static fleet hosts available")
	}
	info := p.instances[p.nextLaunch]
	p.nextLaunch++
	return &info, nil
}

func (p *staticFleetProvisioner) TerminateInstance(context.Context, string) error {
	return nil
}

func (p *staticFleetProvisioner) StopInstance(context.Context, string) error {
	return nil
}

func (p *staticFleetProvisioner) StartInstance(_ context.Context, instanceID string) (*instance.InstanceInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.instances {
		if p.instances[i].InstanceID == instanceID {
			info := p.instances[i]
			return &info, nil
		}
	}
	return nil, fmt.Errorf("unknown static instance %q", instanceID)
}

func (p *staticFleetProvisioner) DescribeInstance(_ context.Context, instanceID string) (*instance.InstanceStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i := range p.instances {
		if p.instances[i].InstanceID == instanceID {
			return &instance.InstanceStatus{
				InstanceID: instanceID,
				State:      instance.CloudInstanceRunning,
				PrivateIP:  p.instances[i].PrivateIP,
			}, nil
		}
	}
	return nil, fmt.Errorf("unknown static instance %q", instanceID)
}

func (p *staticFleetProvisioner) ListInstances(_ context.Context, _ instance.InstanceFilter) ([]instance.InstanceInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]instance.InstanceInfo, len(p.instances))
	copy(out, p.instances)
	return out, nil
}

type fleetNodeClientAdapter struct {
	control.SandboxControl
	sandboxService api.SandboxService
}

func (c *fleetNodeClientAdapter) HealthCheck(ctx context.Context) (*fleet.HealthCheckResult, error) {
	resp, err := c.sandboxService.HealthCheck(ctx, &api.HealthCheckRequest{})
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return nil, fmt.Errorf("sandbox health check returned nil response")
	}
	return &fleet.HealthCheckResult{
		Status:       resp.Status,
		SessionCount: resp.SessionCount,
		ActiveTools:  resp.ActiveTools,
		Uptime:       resp.Uptime,
		Errors:       append([]string(nil), resp.Errors...),
	}, nil
}

// staticHostControl implements SandboxControl for a pre-existing host that
// already has the agent running. It skips all provisioning — CreateSandbox
// and LaunchProcess return the static address, and destroy/kill are no-ops.
type staticHostControl struct {
	addr string // host:port where the agent is listening
}

var _ control.SandboxControl = (*staticHostControl)(nil)

func newStaticHostControl(addr string) *staticHostControl {
	return &staticHostControl{addr: addr}
}

func (s *staticHostControl) CreateSandbox(_ context.Context, _ control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	return &control.CreateSandboxResponse{
		SandboxID: "static-host",
		Address:   s.addr,
	}, nil
}

func (s *staticHostControl) DestroySandbox(_ context.Context, _ string) error {
	return nil // no-op: we don't own the host
}

func (s *staticHostControl) LaunchProcess(_ context.Context, req control.LaunchProcessRequest) (*control.LaunchProcessResponse, error) {
	return &control.LaunchProcessResponse{
		ProcessID: "static",
		Address:   s.addr,
		Status:    control.ProcessRunning,
	}, nil
}

func (s *staticHostControl) KillProcess(_ context.Context, _ control.KillProcessRequest) error {
	return nil // no-op: we don't own the process
}

func (s *staticHostControl) GetProcessStatus(_ context.Context, _ control.GetProcessStatusRequest) (*control.GetProcessStatusResponse, error) {
	return &control.GetProcessStatusResponse{
		Status: control.ProcessRunning,
	}, nil
}

func (s *staticHostControl) PauseSandbox(_ context.Context, _ string) error {
	return fmt.Errorf("pause not supported for static direct host")
}

func (s *staticHostControl) ResumeSandbox(_ context.Context, _ string) error {
	return fmt.Errorf("resume not supported for static direct host")
}

func (s *staticHostControl) Capabilities() control.SandboxCapabilities {
	return control.SandboxCapabilities{
		LaunchProcess: true,
	}
}
