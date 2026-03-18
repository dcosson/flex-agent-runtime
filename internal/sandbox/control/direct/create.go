package direct

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"text/template"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

const (
	createPollInitialDelay = 2 * time.Second
	createPollMaxDelay     = 10 * time.Second
	defaultRootVolumeSize  = 50
)

// CreateSandbox provisions a new EC2 instance and returns it as a sandbox.
func (d *DirectSandboxControl) CreateSandbox(ctx context.Context, req control.CreateSandboxRequest) (*control.CreateSandboxResponse, error) {
	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		return nil, ErrDirectClosed
	}
	d.inflightWg.Add(1)
	d.mu.Unlock()
	defer d.inflightWg.Done()

	instanceType, err := selectInstanceType(req.Resources, d.config.DefaultInstanceType)
	if err != nil {
		return nil, err
	}

	userData, err := renderUserData(d.config.UserDataTemplate, UserDataTemplateData{
		SandboxID:    encodeSandboxID("pending"),
		Labels:       cloneStringMap(req.Labels),
		WorkspaceDir: "/workspace",
	})
	if err != nil {
		return nil, fmt.Errorf("direct: render user-data template: %w", err)
	}

	image := d.config.AMIID
	if req.Template != "" {
		image = req.Template
	}

	launchCfg := instance.InstanceConfig{
		Image:        image,
		InstanceType: instanceType,
		UserData:     userData,
		Tags:         mergeCreateTags(d.config.Tags, req.Labels),
		DiskSizeGB:   d.config.RootVolumeSizeGB,
	}
	if launchCfg.DiskSizeGB <= 0 {
		launchCfg.DiskSizeGB = defaultRootVolumeSize
	}

	info, err := d.provisioner.LaunchInstance(ctx, launchCfg)
	if err != nil {
		return nil, err
	}
	if info == nil || info.InstanceID == "" {
		return nil, fmt.Errorf("direct: launch returned empty instance ID")
	}

	instanceID := info.InstanceID
	sandboxID := encodeSandboxID(instanceID)
	deadline := time.Now().Add(d.config.InstanceReadyTimeout)

	status, err := d.waitForInstanceReady(ctx, instanceID, deadline)
	if err != nil {
		d.terminateOrphanInstance(instanceID)
		return nil, err
	}

	if err := d.waitForSSMRegistration(ctx, instanceID, deadline); err != nil {
		d.terminateOrphanInstance(instanceID)
		return nil, err
	}

	d.mu.Lock()
	if d.closing {
		d.mu.Unlock()
		d.terminateOrphanInstance(instanceID)
		return nil, ErrDirectClosed
	}

	d.instances[sandboxID] = &instanceState{
		instanceID: instanceID,
		publicIP:   status.PublicIP,
		privateIP:  status.PrivateIP,
		status:     statusRunning,
		processes:  make(map[string]*processState),
	}
	d.mu.Unlock()

	addressIP := selectAddressIP(status.PublicIP, status.PrivateIP, d.config.IPSelectionMode)

	return &control.CreateSandboxResponse{
		SandboxID:    sandboxID,
		Address:      fmt.Sprintf("%s:0", addressIP),
		Capabilities: d.Capabilities(),
	}, nil
}

func (d *DirectSandboxControl) waitForInstanceReady(ctx context.Context, instanceID string, deadline time.Time) (*instance.InstanceStatus, error) {
	delay := createPollInitialDelay
	for {
		if err := d.pollPreflight(ctx); err != nil {
			return nil, err
		}

		status, err := d.provisioner.DescribeInstance(ctx, instanceID)
		if err == nil && status != nil && status.State == instance.CloudInstanceRunning {
			if selectAddressIP(status.PublicIP, status.PrivateIP, d.config.IPSelectionMode) != "" {
				return status, nil
			}
		}

		if time.Now().After(deadline) {
			return nil, ErrInstanceReadyTimeout
		}
		if err := d.pollSleep(ctx, delay, deadline); err != nil {
			return nil, err
		}
		delay = nextBackoffDelay(delay, createPollMaxDelay)
	}
}

func (d *DirectSandboxControl) waitForSSMRegistration(ctx context.Context, instanceID string, deadline time.Time) error {
	delay := createPollInitialDelay
	for {
		if err := d.pollPreflight(ctx); err != nil {
			return err
		}

		out, err := d.ssmClient.DescribeInstanceInformation(ctx, &ssm.DescribeInstanceInformationInput{
			Filters: []ssmtypes.InstanceInformationStringFilter{
				{
					Key:    aws.String("InstanceIds"),
					Values: []string{instanceID},
				},
			},
		})
		if err == nil && out != nil && len(out.InstanceInformationList) > 0 {
			return nil
		}

		if time.Now().After(deadline) {
			return ErrSSMUnavailable
		}
		if err := d.pollSleep(ctx, delay, deadline); err != nil {
			if errors.Is(err, ErrInstanceReadyTimeout) {
				return ErrSSMUnavailable
			}
			return err
		}
		delay = nextBackoffDelay(delay, createPollMaxDelay)
	}
}

func (d *DirectSandboxControl) pollPreflight(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	closing := d.closing
	d.mu.Unlock()
	if closing {
		return ErrDirectClosed
	}
	return nil
}

func (d *DirectSandboxControl) pollSleep(ctx context.Context, baseDelay time.Duration, deadline time.Time) error {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return ErrInstanceReadyTimeout
	}

	sleepFor := jitterDelay(baseDelay)
	if sleepFor > remaining {
		sleepFor = remaining
	}
	if sleepFor <= 0 {
		return nil
	}

	timer := time.NewTimer(sleepFor)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func nextBackoffDelay(current, max time.Duration) time.Duration {
	if current >= max {
		return max
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func jitterDelay(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	jitter := base / 5
	if jitter <= 0 {
		return base
	}
	delta := time.Duration(rand.Int63n(int64(2*jitter+1))) - jitter
	return base + delta
}

func renderUserData(tpl string, data UserDataTemplateData) (string, error) {
	if tpl == "" {
		return "", nil
	}
	parsed, err := template.New("direct-user-data").Option("missingkey=zero").Parse(tpl)
	if err != nil {
		return "", err
	}
	var out bytes.Buffer
	if err := parsed.Execute(&out, data); err != nil {
		return "", err
	}
	return out.String(), nil
}

func mergeCreateTags(base, labels map[string]string) map[string]string {
	tags := make(map[string]string, len(base)+len(labels)+2)
	for k, v := range base {
		tags[k] = v
	}
	for k, v := range labels {
		tags[k] = v
	}
	tags["ManagedBy"] = "flex-agent-runtime"
	tags["adapter"] = "direct"
	return tags
}

func selectAddressIP(publicIP, privateIP, mode string) string {
	switch mode {
	case "private":
		if privateIP != "" {
			return privateIP
		}
		return publicIP
	case "public", "auto", "":
		if publicIP != "" {
			return publicIP
		}
		return privateIP
	default:
		if publicIP != "" {
			return publicIP
		}
		return privateIP
	}
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func (d *DirectSandboxControl) terminateOrphanInstance(instanceID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := d.provisioner.TerminateInstance(ctx, instanceID); err != nil {
		d.logger.Warn("failed to terminate orphan instance", "instance_id", instanceID, "error", err)
	}
}
