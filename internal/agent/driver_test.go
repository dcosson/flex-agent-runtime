package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/dcosson/flex-agent-runtime/internal/ai"
)

type testDriver struct{}

func (d *testDriver) Start(context.Context, *Session, string) error { return nil }
func (d *testDriver) Resume(context.Context, *Session) error        { return nil }
func (d *testDriver) Stop(context.Context) error                    { return nil }
func (d *testDriver) Subscribe(func(AgentEvent)) func()             { return func() {} }

func TestDriverRegistry(t *testing.T) {
	name := "unit-test-driver"
	err := RegisterDriver(name, func(cfg DriverConfig) (AgentDriver, error) {
		if cfg.Model.ID != "m" {
			return nil, errors.New("bad cfg")
		}
		return &testDriver{}, nil
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	d, err := NewDriver(name, DriverConfig{Model: ai.Model{ID: "m"}})
	if err != nil {
		t.Fatalf("new driver: %v", err)
	}
	if d == nil {
		t.Fatalf("expected driver instance")
	}

	if err := RegisterDriver(name, func(DriverConfig) (AgentDriver, error) { return nil, nil }); err == nil {
		t.Fatalf("expected duplicate registration error")
	}
}
