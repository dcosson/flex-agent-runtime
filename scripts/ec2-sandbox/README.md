# EC2 Provisioning Scripts

Provision disposable EC2 instances for flexagent. Supports two roles:

- **sandbox-host**: ZFS + gVisor isolation, runs `flexagent serve sandbox-host`
- **orchestrator**: Minimal setup, runs `flexagent serve all`

## Files

- `provision.sh`: Create SG + EC2 + bootstrap + deploy/start service.
- `teardown.sh`: Terminate instance and delete SG.
- `iam-policy.json`: IAM policy for the `flexagent-ec2` user.

## Quick Start

Provision a sandbox-host:

```bash
scripts/ec2-sandbox/provision.sh \
  --flex-role sandbox-host \
  --key-name flexagent-ec2-key \
  --ssh-key-path ~/.ssh/flexagent-ec2-key.pem
```

Provision an orchestrator:

```bash
scripts/ec2-sandbox/provision.sh \
  --flex-role orchestrator \
  --key-name flexagent-ec2-key \
  --ssh-key-path ~/.ssh/flexagent-ec2-key.pem
```

The script prints instance ID, public/private IPs, and RPC endpoint. For sandbox-host, it also prints ZFS pool and dataset info.

## Teardown

```bash
scripts/ec2-sandbox/teardown.sh --flex-role sandbox-host
scripts/ec2-sandbox/teardown.sh --flex-role orchestrator
```

`teardown.sh` reads `.last_provision_<role>.env` by default and cleans up those resources.

## AWS Setup

### Prerequisites

Install the AWS CLI and log in with root/admin credentials:

```bash
brew install awscli
aws configure  # enter your root or admin access key, region, and output format
```

If you don't have an access key yet, log into the AWS Console → click your account name (top right) → Security credentials → Access keys → Create access key.

### 1. Create an EC2 key pair for SSH

Run this with your root/admin credentials (no profile flag needed — key pairs are account-level):

```bash
aws ec2 create-key-pair \
  --key-name flexagent-ec2-key \
  --query 'KeyMaterial' \
  --output text > ~/.ssh/flexagent-ec2-key.pem

chmod 400 ~/.ssh/flexagent-ec2-key.pem
```

### 2. Create an IAM user and attach policy

Still using root/admin credentials:

```bash
aws iam create-user --user-name flexagent-ec2

aws iam create-policy \
  --policy-name FlexAgentEC2Provisioning \
  --policy-document file://scripts/ec2-sandbox/iam-policy.json

aws iam attach-user-policy \
  --user-name flexagent-ec2 \
  --policy-arn arn:aws:iam::<ACCOUNT_ID>:policy/FlexAgentEC2Provisioning
```

### 3. Create access key and configure profile

```bash
aws iam create-access-key --user-name flexagent-ec2
aws configure --profile flexagent-ec2
```

Enter the access key ID and secret from the output above. Use the same region as your default profile.

### 4. Run scripts with the profile

From here on, use the scoped-down profile:

```bash
AWS_PROFILE=flexagent-ec2 scripts/ec2-sandbox/provision.sh \
  --flex-role sandbox-host \
  --key-name flexagent-ec2-key \
  --ssh-key-path ~/.ssh/flexagent-ec2-key.pem
```

See `iam-policy.json` for the full policy. For more secure setups, consider AWS SSO (`aws configure sso`) or assume-role instead of long-lived access keys.

### Instance profile for SSM

Launched instances need **AmazonSSMManagedInstanceCore** so the SSM agent can receive commands. Create an instance profile with a role that has this policy, and pass it via `--iam-instance-profile`. The deployer policy allows `iam:PassRole` for roles matching `flexagent-sandbox-*`.

## Connecting orchestrator to sandbox-host

The provision script sets up each instance independently. To connect an orchestrator to a sandbox-host, SSH into the orchestrator and add the sandbox-host address to its config:

```bash
ssh -i ~/.ssh/flexagent-ec2-key.pem ubuntu@<orchestrator-public-ip>
sudo vi /etc/default/flexagent-orchestrator
# Add: SANDBOX_HOST_ADDR=<sandbox-host-private-ip>:8080
sudo systemctl restart flexagent-orchestrator
```

Use the sandbox-host's **private IP** if both instances are in the same VPC (avoids NAT and is faster). The private IP is printed by the provision script and saved in `.last_provision_<role>.env`.

## Notes

- Defaults are intentionally simple for manual testing, not hardened production.
- RPC ingress defaults to `0.0.0.0/0`; restrict with `--rpc-cidr` for safer testing.
- SSH ingress defaults to your detected public IP `/32`; override with `--ssh-cidr`.
