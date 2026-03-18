//go:build integration_ec2

package direct

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

const (
	integrationTestSuiteTag = "integration-ec2"
	integrationSweepMaxAge  = 1 * time.Hour
)

type integrationEC2Config struct {
	Region              string
	AMIID               string
	SubnetID            string
	SecurityGroupID     string
	InstanceProfileARN  string
	DefaultInstanceType string
}

type integrationEC2Harness struct {
	cfg      integrationEC2Config
	testRun  string
	now      func() time.Time
	sweepAge time.Duration
}

func newIntegrationEC2Harness(t *testing.T) *integrationEC2Harness {
	t.Helper()

	cfg := integrationEC2Config{
		Region:              os.Getenv("AWS_REGION"),
		AMIID:               os.Getenv("DIRECT_EC2_AMI_ID"),
		SubnetID:            os.Getenv("DIRECT_EC2_SUBNET_ID"),
		SecurityGroupID:     os.Getenv("DIRECT_EC2_SECURITY_GROUP_ID"),
		InstanceProfileARN:  os.Getenv("DIRECT_EC2_INSTANCE_PROFILE_ARN"),
		DefaultInstanceType: getenvDefault("DIRECT_EC2_INSTANCE_TYPE", "t3.medium"),
	}

	if cfg.Region == "" || cfg.AMIID == "" || cfg.SubnetID == "" || cfg.SecurityGroupID == "" || cfg.InstanceProfileARN == "" {
		t.Skip("integration_ec2 requires AWS_REGION, DIRECT_EC2_AMI_ID, DIRECT_EC2_SUBNET_ID, DIRECT_EC2_SECURITY_GROUP_ID, DIRECT_EC2_INSTANCE_PROFILE_ARN")
	}

	return &integrationEC2Harness{
		cfg:      cfg,
		testRun:  fmt.Sprintf("direct-int-%d", time.Now().UnixNano()),
		now:      time.Now,
		sweepAge: integrationSweepMaxAge,
	}
}

func (h *integrationEC2Harness) testTags() map[string]string {
	return map[string]string{
		"ManagedBy":       "flex-agent-runtime",
		"adapter":         "direct",
		"flex-test-suite": integrationTestSuiteTag,
		"flex-test-run":   h.testRun,
	}
}

func (h *integrationEC2Harness) createTaggedSandbox(ctx context.Context, d *DirectSandboxControl, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	labels := make(map[string]string, len(req.Labels)+len(h.testTags()))
	for k, v := range req.Labels {
		labels[k] = v
	}
	for k, v := range h.testTags() {
		labels[k] = v
	}
	req.Labels = labels
	return d.CreateSandbox(ctx, req)
}

func (h *integrationEC2Harness) sweepLeakedInstances(ctx context.Context, provisioner instance.InstanceProvisioner) (int, error) {
	infos, err := provisioner.ListInstances(ctx, instance.InstanceFilter{
		Tags: map[string]string{
			"ManagedBy":       "flex-agent-runtime",
			"adapter":         "direct",
			"flex-test-suite": integrationTestSuiteTag,
		},
	})
	if err != nil {
		return 0, err
	}

	swept := 0
	cutoff := h.now().Add(-h.sweepAge)
	for _, info := range infos {
		if info.InstanceID == "" {
			continue
		}
		if !info.LaunchTime.IsZero() && info.LaunchTime.After(cutoff) {
			continue
		}
		if termErr := provisioner.TerminateInstance(ctx, info.InstanceID); termErr != nil {
			return swept, termErr
		}
		swept++
	}
	return swept, nil
}

func TestIntegrationEC2_FullLifecycle(t *testing.T) {
	_ = newIntegrationEC2Harness(t)
	t.Skip("integration_ec2 skeleton: wire real EC2 provisioner and SSM client")
}

func TestIntegrationEC2_MultiAgent(t *testing.T) {
	_ = newIntegrationEC2Harness(t)
	t.Skip("integration_ec2 skeleton: wire real EC2 provisioner and SSM client")
}

func TestIntegrationEC2_PauseResume(t *testing.T) {
	_ = newIntegrationEC2Harness(t)
	t.Skip("integration_ec2 skeleton: wire real EC2 provisioner and SSM client")
}

func TestIntegrationEC2_CrashRecovery(t *testing.T) {
	_ = newIntegrationEC2Harness(t)
	t.Skip("integration_ec2 skeleton: wire real EC2 provisioner and SSM client")
}

func TestIntegrationEC2_NetworkConnectivity(t *testing.T) {
	_ = newIntegrationEC2Harness(t)
	t.Skip("integration_ec2 skeleton: wire real EC2 provisioner and SSM client")
}

func getenvDefault(name, defaultValue string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return defaultValue
}
