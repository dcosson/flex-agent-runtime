package api

import (
	"errors"
	"io"
	"sync"
)

var ErrStreamClosed = errors.New("rpc stream closed")

type bufferedExecuteToolStream struct {
	mu     sync.Mutex
	msgs   []*ExecuteToolStreamMessage
	closed bool
}

type chanExecuteToolStream struct {
	ch     <-chan *ExecuteToolStreamMessage
	errCh  <-chan error
	close  func() error
	closed bool
	mu     sync.Mutex
}

func NewExecuteToolStream(messages ...*ExecuteToolStreamMessage) ExecuteToolStreamReceiver {
	cp := make([]*ExecuteToolStreamMessage, 0, len(messages))
	cp = append(cp, messages...)
	return &bufferedExecuteToolStream{msgs: cp}
}

func NewExecuteToolStreamChannel(ch <-chan *ExecuteToolStreamMessage, closeFn func() error) ExecuteToolStreamReceiver {
	return NewExecuteToolStreamChannelWithErr(ch, nil, closeFn)
}

func NewExecuteToolStreamChannelWithErr(ch <-chan *ExecuteToolStreamMessage, errCh <-chan error, closeFn func() error) ExecuteToolStreamReceiver {
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	return &chanExecuteToolStream{ch: ch, errCh: errCh, close: closeFn}
}

func (s *bufferedExecuteToolStream) Recv() (*ExecuteToolStreamMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, ErrStreamClosed
	}
	if len(s.msgs) == 0 {
		return nil, io.EOF
	}
	msg := s.msgs[0]
	s.msgs = s.msgs[1:]
	return msg, nil
}

func (s *bufferedExecuteToolStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *chanExecuteToolStream) Recv() (*ExecuteToolStreamMessage, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, ErrStreamClosed
	}
	msg, ok := <-s.ch
	if !ok {
		if s.errCh != nil {
			select {
			case err := <-s.errCh:
				if err != nil {
					return nil, err
				}
			default:
			}
		}
		return nil, io.EOF
	}
	return msg, nil
}

func (s *chanExecuteToolStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	return s.close()
}

type chanEventReceiver struct {
	ch     <-chan *AgentEventEnvelope
	close  func() error
	closed bool
	mu     sync.Mutex
}

func NewAgentEventReceiver(ch <-chan *AgentEventEnvelope, closeFn func() error) AgentEventReceiver {
	if closeFn == nil {
		closeFn = func() error { return nil }
	}
	return &chanEventReceiver{ch: ch, close: closeFn}
}

func (r *chanEventReceiver) Recv() (*AgentEventEnvelope, error) {
	r.mu.Lock()
	closed := r.closed
	r.mu.Unlock()
	if closed {
		return nil, ErrStreamClosed
	}
	evt, ok := <-r.ch
	if !ok {
		return nil, io.EOF
	}
	return evt, nil
}

func (r *chanEventReceiver) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	return r.close()
}
