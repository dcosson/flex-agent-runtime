package direct

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
)

// SSMAPI wraps the subset of SSM SDK methods used by this adapter for remote
// process management. This is the remaining EC2-specific piece -- instance
// lifecycle is provider-neutral via InstanceProvisioner. In the future, this
// could be abstracted behind a RemoteExec interface to support non-AWS
// providers.
type SSMAPI interface {
	SendCommand(ctx context.Context, input *ssm.SendCommandInput, opts ...func(*ssm.Options)) (*ssm.SendCommandOutput, error)
	GetCommandInvocation(ctx context.Context, input *ssm.GetCommandInvocationInput, opts ...func(*ssm.Options)) (*ssm.GetCommandInvocationOutput, error)
	DescribeInstanceInformation(ctx context.Context, input *ssm.DescribeInstanceInformationInput, opts ...func(*ssm.Options)) (*ssm.DescribeInstanceInformationOutput, error)
}
