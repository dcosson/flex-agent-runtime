// Package ec2 implements the instance.InstanceProvisioner interface using
// the AWS EC2 API (SDK v2). Provider-specific launch configuration is passed
// via EC2LaunchConfig at construction time, keeping the shared InstanceConfig
// provider-neutral.
package ec2

import (
	"context"
	"encoding/base64"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control/instance"
)

// EC2API wraps the subset of the AWS EC2 SDK methods needed by the provisioner.
// This enables unit testing with a mock implementation.
type EC2API interface {
	RunInstances(ctx context.Context, params *ec2.RunInstancesInput, optFns ...func(*ec2.Options)) (*ec2.RunInstancesOutput, error)
	TerminateInstances(ctx context.Context, params *ec2.TerminateInstancesInput, optFns ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
	StopInstances(ctx context.Context, params *ec2.StopInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StopInstancesOutput, error)
	StartInstances(ctx context.Context, params *ec2.StartInstancesInput, optFns ...func(*ec2.Options)) (*ec2.StartInstancesOutput, error)
	DescribeInstances(ctx context.Context, params *ec2.DescribeInstancesInput, optFns ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
}

// EC2LaunchConfig holds AWS-specific instance launch parameters.
// These are provider-specific and are NOT part of the shared InstanceConfig.
type EC2LaunchConfig struct {
	// SecurityGroupIDs are VPC security groups to attach to launched instances.
	SecurityGroupIDs []string

	// SubnetID is the VPC subnet to launch into.
	SubnetID string

	// KeyName is the SSH key pair name (optional, for debugging).
	KeyName string

	// InstanceProfileName is the IAM instance profile NAME (not ARN, not role name).
	// This is the instance profile that gets associated with the EC2 instance via
	// the IamInstanceProfile.Name parameter in RunInstances. The instance profile
	// must already exist and have the desired IAM role attached to it.
	InstanceProfileName string
}

// EC2InstanceProvisioner implements instance.InstanceProvisioner using the
// AWS EC2 API.
type EC2InstanceProvisioner struct {
	client    EC2API
	launchCfg EC2LaunchConfig
	logger    *slog.Logger
}

// managedByTagKey is the standard tag key applied to all instances launched
// by this provisioner for operational visibility and crash recovery.
const managedByTagKey = "ManagedBy"

// managedByTagValue is the standard tag value for the ManagedBy tag.
const managedByTagValue = "flex-agent-runtime"

// NewEC2InstanceProvisioner creates an EC2InstanceProvisioner with the given
// EC2 API client and launch configuration.
func NewEC2InstanceProvisioner(client EC2API, launchCfg EC2LaunchConfig) *EC2InstanceProvisioner {
	return &EC2InstanceProvisioner{
		client:    client,
		launchCfg: launchCfg,
		logger:    slog.Default(),
	}
}

// NewEC2InstanceProvisionerWithLogger creates an EC2InstanceProvisioner with
// a custom logger.
func NewEC2InstanceProvisionerWithLogger(client EC2API, launchCfg EC2LaunchConfig, logger *slog.Logger) *EC2InstanceProvisioner {
	if logger == nil {
		logger = slog.Default()
	}
	return &EC2InstanceProvisioner{
		client:    client,
		launchCfg: launchCfg,
		logger:    logger,
	}
}

// LaunchInstance provisions a new EC2 instance. It calls RunInstances and
// returns immediately with the instance ID and initial info. The instance
// may still be booting (State == CloudInstancePending).
func (p *EC2InstanceProvisioner) LaunchInstance(ctx context.Context, cfg instance.InstanceConfig) (*instance.InstanceInfo, error) {
	if cfg.Image == "" {
		return nil, fmt.Errorf("ec2: InstanceConfig.Image is required")
	}
	if cfg.InstanceType == "" {
		return nil, fmt.Errorf("ec2: InstanceConfig.InstanceType is required")
	}

	// Merge caller tags with the mandatory ManagedBy tag.
	tags := mergeTags(cfg.Tags, map[string]string{
		managedByTagKey: managedByTagValue,
	})

	input := &ec2.RunInstancesInput{
		ImageId:      &cfg.Image,
		InstanceType: ec2types.InstanceType(cfg.InstanceType),
		MinCount:     int32Ptr(1),
		MaxCount:     int32Ptr(1),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags:         toEC2Tags(tags),
			},
		},
	}

	// Apply provider-specific launch config.
	if len(p.launchCfg.SecurityGroupIDs) > 0 {
		input.SecurityGroupIds = p.launchCfg.SecurityGroupIDs
	}
	if p.launchCfg.SubnetID != "" {
		input.SubnetId = &p.launchCfg.SubnetID
	}
	if p.launchCfg.KeyName != "" {
		input.KeyName = &p.launchCfg.KeyName
	}
	if p.launchCfg.InstanceProfileName != "" {
		input.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{
			Name: &p.launchCfg.InstanceProfileName,
		}
	}

	// UserData must be base64-encoded for the EC2 API.
	if cfg.UserData != "" {
		encoded := base64.StdEncoding.EncodeToString([]byte(cfg.UserData))
		input.UserData = &encoded
	}

	// Root volume size override.
	if cfg.DiskSizeGB > 0 {
		input.BlockDeviceMappings = []ec2types.BlockDeviceMapping{
			{
				DeviceName: strPtr("/dev/sda1"),
				Ebs: &ec2types.EbsBlockDevice{
					VolumeSize: int32Ptr(int32(cfg.DiskSizeGB)),
				},
			},
		}
	}

	out, err := p.client.RunInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("ec2: RunInstances: %w", err)
	}
	if len(out.Instances) == 0 {
		return nil, fmt.Errorf("ec2: RunInstances returned no instances")
	}

	inst := out.Instances[0]
	info := ec2InstanceToInfo(inst)

	p.logger.Info("launched EC2 instance",
		"instance_id", info.InstanceID,
		"state", info.State,
	)

	return &info, nil
}

// TerminateInstance permanently destroys an EC2 instance.
// Terminating an already-terminated instance is idempotent (no error).
func (p *EC2InstanceProvisioner) TerminateInstance(ctx context.Context, instanceID string) error {
	_, err := p.client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		// EC2 returns an error for instances that are already terminated in some
		// edge cases. We treat this as success for idempotency.
		// In practice, EC2 TerminateInstances on an already-terminated instance
		// usually succeeds, but we handle errors gracefully.
		p.logger.Warn("TerminateInstances returned error, checking if already terminated",
			"instance_id", instanceID,
			"error", err,
		)
		// Attempt to verify the instance is terminated/terminating.
		status, descErr := p.DescribeInstance(ctx, instanceID)
		if descErr != nil {
			return fmt.Errorf("ec2: TerminateInstances: %w", err)
		}
		if status.State == instance.CloudInstanceTerminated || status.State == instance.CloudInstanceTerminating {
			return nil // Already terminated, idempotent success.
		}
		return fmt.Errorf("ec2: TerminateInstances: %w", err)
	}

	p.logger.Info("terminated EC2 instance", "instance_id", instanceID)
	return nil
}

// StopInstance stops an EC2 instance without terminating it.
func (p *EC2InstanceProvisioner) StopInstance(ctx context.Context, instanceID string) error {
	_, err := p.client.StopInstances(ctx, &ec2.StopInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return fmt.Errorf("ec2: StopInstances: %w", err)
	}

	p.logger.Info("stopped EC2 instance", "instance_id", instanceID)
	return nil
}

// StartInstance restarts a previously stopped EC2 instance. Returns updated
// InstanceInfo with potentially new IP addresses.
func (p *EC2InstanceProvisioner) StartInstance(ctx context.Context, instanceID string) (*instance.InstanceInfo, error) {
	_, err := p.client.StartInstances(ctx, &ec2.StartInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return nil, fmt.Errorf("ec2: StartInstances: %w", err)
	}

	// After starting, describe to get the updated info (IPs may change).
	status, err := p.DescribeInstance(ctx, instanceID)
	if err != nil {
		return nil, fmt.Errorf("ec2: DescribeInstances after start: %w", err)
	}

	info := &instance.InstanceInfo{
		InstanceID: status.InstanceID,
		PrivateIP:  status.PrivateIP,
		PublicIP:   status.PublicIP,
		State:      status.State,
	}

	p.logger.Info("started EC2 instance",
		"instance_id", instanceID,
		"state", info.State,
	)

	return info, nil
}

// DescribeInstance returns the current status of an EC2 instance.
func (p *EC2InstanceProvisioner) DescribeInstance(ctx context.Context, instanceID string) (*instance.InstanceStatus, error) {
	out, err := p.client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil {
		return nil, fmt.Errorf("ec2: DescribeInstances: %w", err)
	}

	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			if inst.InstanceId != nil && *inst.InstanceId == instanceID {
				state := mapEC2State(inst.State)
				status := &instance.InstanceStatus{
					InstanceID: instanceID,
					State:      state,
				}
				if inst.PrivateIpAddress != nil {
					status.PrivateIP = *inst.PrivateIpAddress
				}
				if inst.PublicIpAddress != nil {
					status.PublicIP = *inst.PublicIpAddress
				}
				if inst.State != nil && inst.State.Name != "" {
					status.StateReason = string(inst.State.Name)
				}
				return status, nil
			}
		}
	}

	return nil, fmt.Errorf("ec2: instance %q not found in DescribeInstances response", instanceID)
}

// ListInstances returns all instances matching the given filter. Uses EC2 tag
// filters and instance-state-name filters to narrow results. This is the crash
// recovery path for rediscovering managed instances.
func (p *EC2InstanceProvisioner) ListInstances(ctx context.Context, filter instance.InstanceFilter) ([]instance.InstanceInfo, error) {
	var filters []ec2types.Filter

	// Map tag filters to EC2 filter format: tag:<key> = [value].
	for k, v := range filter.Tags {
		filterName := fmt.Sprintf("tag:%s", k)
		filters = append(filters, ec2types.Filter{
			Name:   &filterName,
			Values: []string{v},
		})
	}

	// Map state filters to EC2 instance-state-name filter.
	if len(filter.States) > 0 {
		stateValues := make([]string, 0, len(filter.States))
		for _, s := range filter.States {
			ec2States := reverseMapState(s)
			stateValues = append(stateValues, ec2States...)
		}
		filterName := "instance-state-name"
		filters = append(filters, ec2types.Filter{
			Name:   &filterName,
			Values: stateValues,
		})
	}

	input := &ec2.DescribeInstancesInput{}
	if len(filters) > 0 {
		input.Filters = filters
	}

	out, err := p.client.DescribeInstances(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("ec2: DescribeInstances (list): %w", err)
	}

	var results []instance.InstanceInfo
	for _, res := range out.Reservations {
		for _, inst := range res.Instances {
			// By default, exclude terminated instances unless explicitly requested.
			state := mapEC2State(inst.State)
			if state == instance.CloudInstanceTerminated && !statesContain(filter.States, instance.CloudInstanceTerminated) {
				continue
			}
			results = append(results, ec2InstanceToInfo(inst))
		}
	}

	return results, nil
}

// mapEC2State maps an EC2 InstanceState to the provider-neutral CloudInstanceState.
func mapEC2State(state *ec2types.InstanceState) instance.CloudInstanceState {
	if state == nil {
		return instance.CloudInstancePending
	}
	switch state.Name {
	case ec2types.InstanceStateNamePending:
		return instance.CloudInstancePending
	case ec2types.InstanceStateNameRunning:
		return instance.CloudInstanceRunning
	case ec2types.InstanceStateNameStopping:
		return instance.CloudInstanceStopping
	case ec2types.InstanceStateNameStopped:
		return instance.CloudInstanceStopped
	case ec2types.InstanceStateNameShuttingDown:
		return instance.CloudInstanceTerminating
	case ec2types.InstanceStateNameTerminated:
		return instance.CloudInstanceTerminated
	default:
		return instance.CloudInstancePending
	}
}

// reverseMapState maps a CloudInstanceState back to EC2 instance-state-name
// values. Some CloudInstanceStates map to multiple EC2 states (e.g.,
// CloudInstanceTerminating -> "shutting-down").
func reverseMapState(s instance.CloudInstanceState) []string {
	switch s {
	case instance.CloudInstancePending:
		return []string{"pending"}
	case instance.CloudInstanceRunning:
		return []string{"running"}
	case instance.CloudInstanceStopping:
		return []string{"stopping"}
	case instance.CloudInstanceStopped:
		return []string{"stopped"}
	case instance.CloudInstanceTerminating:
		return []string{"shutting-down"}
	case instance.CloudInstanceTerminated:
		return []string{"terminated"}
	default:
		return nil
	}
}

// ec2InstanceToInfo converts an EC2 Instance to an InstanceInfo.
func ec2InstanceToInfo(inst ec2types.Instance) instance.InstanceInfo {
	info := instance.InstanceInfo{
		State: mapEC2State(inst.State),
		Tags:  fromEC2Tags(inst.Tags),
	}
	if inst.InstanceId != nil {
		info.InstanceID = *inst.InstanceId
	}
	if inst.PrivateIpAddress != nil {
		info.PrivateIP = *inst.PrivateIpAddress
	}
	if inst.PublicIpAddress != nil {
		info.PublicIP = *inst.PublicIpAddress
	}
	if inst.LaunchTime != nil {
		info.LaunchTime = *inst.LaunchTime
	}
	return info
}

// toEC2Tags converts a map[string]string to EC2 Tag slice.
func toEC2Tags(tags map[string]string) []ec2types.Tag {
	result := make([]ec2types.Tag, 0, len(tags))
	for k, v := range tags {
		key := k
		val := v
		result = append(result, ec2types.Tag{
			Key:   &key,
			Value: &val,
		})
	}
	return result
}

// fromEC2Tags converts an EC2 Tag slice to map[string]string.
func fromEC2Tags(tags []ec2types.Tag) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	result := make(map[string]string, len(tags))
	for _, t := range tags {
		if t.Key != nil && t.Value != nil {
			result[*t.Key] = *t.Value
		}
	}
	return result
}

// mergeTags creates a new map containing all entries from both maps. If both
// maps contain the same key, the value from overrides wins.
func mergeTags(base, overrides map[string]string) map[string]string {
	result := make(map[string]string, len(base)+len(overrides))
	for k, v := range base {
		result[k] = v
	}
	for k, v := range overrides {
		result[k] = v
	}
	return result
}

// statesContain reports whether the slice contains the given state.
func statesContain(states []instance.CloudInstanceState, target instance.CloudInstanceState) bool {
	for _, s := range states {
		if s == target {
			return true
		}
	}
	return false
}

func int32Ptr(v int32) *int32 { return &v }
func strPtr(v string) *string { return &v }
