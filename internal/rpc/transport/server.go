package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"connectrpc.com/connect"
	"h2-agent-runtime/internal/rpc"
	"h2-agent-runtime/internal/rpc/api"
	"h2-agent-runtime/internal/termmux"
)

type ServerConfig struct {
	MaxMessageBytes int
	APIVersion      string
	MinAPIVersion   string
	AuthHook        ServerAuthHook
}

type Server struct {
	sandbox api.SandboxService
	events  api.AgentEventService
	terms   *termmux.SessionManager
	cfg     ServerConfig

	handlerOnce sync.Once
	handler     http.Handler
	subSeq      atomic.Uint64
}

func NewServer(sandbox api.SandboxService, events api.AgentEventService, terms *termmux.SessionManager, cfg ServerConfig) *Server {
	if cfg.MaxMessageBytes <= 0 {
		cfg.MaxMessageBytes = 16 << 20
	}
	if cfg.APIVersion == "" {
		cfg.APIVersion = "v1"
	}
	if cfg.MinAPIVersion == "" {
		cfg.MinAPIVersion = rpc.MinSupportedAPIVersion()
	}
	return &Server{sandbox: sandbox, events: events, terms: terms, cfg: cfg}
}

func (s *Server) Handler() http.Handler {
	s.handlerOnce.Do(func() {
		mux := http.NewServeMux()
		opts := []connect.HandlerOption{
			connect.WithCodec(JSONCodec{}),
			connect.WithReadMaxBytes(s.cfg.MaxMessageBytes),
			connect.WithSendMaxBytes(s.cfg.MaxMessageBytes),
			connect.WithInterceptors(NewPolicyInterceptor(InterceptorConfig{
				AuthHook:      s.cfg.AuthHook,
				APIVersion:    s.cfg.APIVersion,
				MinAPIVersion: s.cfg.MinAPIVersion,
			})),
		}

		mux.Handle(ProcedureSandboxCreateSession, connect.NewUnaryHandlerSimple(
			ProcedureSandboxCreateSession, func(ctx context.Context, req *api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
				res, err := s.sandbox.CreateSession(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxGetSession, connect.NewUnaryHandlerSimple(
			ProcedureSandboxGetSession, func(ctx context.Context, req *api.GetSessionRequest) (*api.GetSessionResponse, error) {
				res, err := s.sandbox.GetSession(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxPauseSession, connect.NewUnaryHandlerSimple(
			ProcedureSandboxPauseSession, func(ctx context.Context, req *api.PauseSessionRequest) (*api.PauseSessionResponse, error) {
				res, err := s.sandbox.PauseSession(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxResumeSession, connect.NewUnaryHandlerSimple(
			ProcedureSandboxResumeSession, func(ctx context.Context, req *api.ResumeSessionRequest) (*api.ResumeSessionResponse, error) {
				res, err := s.sandbox.ResumeSession(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxDestroySession, connect.NewUnaryHandlerSimple(
			ProcedureSandboxDestroySession, func(ctx context.Context, req *api.DestroySessionRequest) (*api.DestroySessionResponse, error) {
				res, err := s.sandbox.DestroySession(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxExecuteTool, connect.NewUnaryHandlerSimple(
			ProcedureSandboxExecuteTool, func(ctx context.Context, req *api.ExecuteToolRequest) (*api.ExecuteToolResponse, error) {
				res, err := s.sandbox.ExecuteTool(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxTurnComplete, connect.NewUnaryHandlerSimple(
			ProcedureSandboxTurnComplete, func(ctx context.Context, req *api.TurnCompleteRequest) (*api.TurnCompleteResponse, error) {
				res, err := s.sandbox.TurnComplete(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxCreateSnapshot, connect.NewUnaryHandlerSimple(
			ProcedureSandboxCreateSnapshot, func(ctx context.Context, req *api.CreateSnapshotRequest) (*api.CreateSnapshotResponse, error) {
				res, err := s.sandbox.CreateSnapshot(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxRollback, connect.NewUnaryHandlerSimple(
			ProcedureSandboxRollback, func(ctx context.Context, req *api.RollbackSessionRequest) (*api.RollbackSessionResponse, error) {
				res, err := s.sandbox.RollbackSession(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxListSnapshots, connect.NewUnaryHandlerSimple(
			ProcedureSandboxListSnapshots, func(ctx context.Context, req *api.ListSnapshotsRequest) (*api.ListSnapshotsResponse, error) {
				res, err := s.sandbox.ListSnapshots(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxHealthCheck, connect.NewUnaryHandlerSimple(
			ProcedureSandboxHealthCheck, func(ctx context.Context, req *api.HealthCheckRequest) (*api.HealthCheckResponse, error) {
				res, err := s.sandbox.HealthCheck(ctx, req)
				return res, toConnectError(err)
			}, opts...,
		))
		mux.Handle(ProcedureSandboxExecuteStream, connect.NewServerStreamHandler(
			ProcedureSandboxExecuteStream,
			func(ctx context.Context, req *connect.Request[api.ExecuteToolRequest], stream *connect.ServerStream[api.ExecuteToolStreamMessage]) error {
				recv, err := s.sandbox.ExecuteToolStream(ctx, req.Msg)
				if err != nil {
					return toConnectError(err)
				}
				defer recv.Close()
				for {
					msg, recvErr := recv.Recv()
					if recvErr == nil {
						if sendErr := stream.Send(msg); sendErr != nil {
							return sendErr
						}
						continue
					}
					if errors.Is(recvErr, io.EOF) {
						return nil
					}
					return toConnectError(recvErr)
				}
			},
			opts...,
		))
		mux.Handle(ProcedureEventsStream, connect.NewServerStreamHandler(
			ProcedureEventsStream,
			func(ctx context.Context, req *connect.Request[api.StreamAgentEventsRequest], stream *connect.ServerStream[api.AgentEventEnvelope]) error {
				recv, err := s.events.StreamAgentEvents(ctx, req.Msg)
				if err != nil {
					return toConnectError(err)
				}
				defer recv.Close()
				for {
					msg, recvErr := recv.Recv()
					if recvErr == nil {
						if sendErr := stream.Send(msg); sendErr != nil {
							return sendErr
						}
						continue
					}
					if errors.Is(recvErr, io.EOF) {
						return nil
					}
					return toConnectError(recvErr)
				}
			},
			opts...,
		))
		mux.Handle(ProcedureTerminalStream, connect.NewBidiStreamHandler(
			ProcedureTerminalStream,
			func(ctx context.Context, stream *connect.BidiStream[termmux.TerminalClientMessage, termmux.TerminalServerMessage]) error {
				if s.terms == nil {
					return connect.NewError(connect.CodeUnimplemented, errors.New("terminal service not configured"))
				}
				return s.streamTerminal(ctx, stream)
			},
			opts...,
		))
		s.handler = mux
	})
	return s.handler
}

func (s *Server) streamTerminal(ctx context.Context, stream *connect.BidiStream[termmux.TerminalClientMessage, termmux.TerminalServerMessage]) error {
	first, err := stream.Receive()
	if err != nil {
		return toConnectError(err)
	}
	if first == nil || first.Attach == nil || first.Attach.SessionID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("first message must include attach.session_id"))
	}
	session, err := s.terms.Get(first.Attach.SessionID)
	if err != nil {
		return connect.NewError(connect.CodeNotFound, err)
	}
	subID := "rpc-" + first.Attach.SessionID + "-" + strconv.FormatUint(s.subSeq.Add(1), 10)
	sub := session.SubscribeTerminal(subID)
	defer session.UnsubscribeTerminal(subID)

	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	sendMu := &sync.Mutex{}
	sendLocked := func(msg *termmux.TerminalServerMessage) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return stream.Send(msg)
	}

	if err := sendLocked(&termmux.TerminalServerMessage{
		Attached: &termmux.TerminalAttached{Rows: sub.Rows, Cols: sub.Cols, Scrollback: sub.Scrollback},
	}); err != nil {
		return err
	}

	done := make(chan error, 1)
	sendDone := func(err error) {
		select {
		case done <- err:
		default:
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-streamCtx.Done():
				sendDone(streamCtx.Err())
				return
			case <-sub.Done:
				_ = sendLocked(&termmux.TerminalServerMessage{Detached: &termmux.TerminalDetached{Reason: "session_ended"}})
				sendDone(io.EOF)
				return
			case chunk, ok := <-sub.Chunks:
				if !ok {
					sendDone(io.EOF)
					return
				}
				for start := 0; start < len(chunk); start += termmux.MaxTerminalChunkSize {
					end := start + termmux.MaxTerminalChunkSize
					if end > len(chunk) {
						end = len(chunk)
					}
					if err := sendLocked(&termmux.TerminalServerMessage{
						Output: &termmux.TerminalOutput{Data: append([]byte(nil), chunk[start:end]...)},
					}); err != nil {
						sendDone(err)
						return
					}
				}
			}
		}
	}()

	defer func() {
		streamCancel()
		wg.Wait()
	}()

	limiter := newInputLimiter(termmux.MaxInputRateBytes, termmux.InputBurstBytes)
	for {
		msg, recvErr := stream.Receive()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				return nil
			}
			return recvErr
		}
		if msg == nil {
			continue
		}
		if msg.Input != nil {
			if !limiter.Allow(len(msg.Input.Data)) {
				if err := sendLocked(&termmux.TerminalServerMessage{
					Output: &termmux.TerminalOutput{Data: []byte("[input dropped: rate limit exceeded]\\n")},
				}); err != nil {
					return err
				}
				continue
			}
			if _, err := session.WritePTY(msg.Input.Data); err != nil {
				return toConnectError(err)
			}
		}
		if msg.Resize != nil {
			session.Resize(msg.Resize.Rows, msg.Resize.Cols)
		}
		select {
		case streamErr := <-done:
			if errors.Is(streamErr, io.EOF) || errors.Is(streamErr, context.Canceled) {
				return nil
			}
			return streamErr
		default:
		}
	}
}
