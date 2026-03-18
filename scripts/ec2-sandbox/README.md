# EC2 Sandbox Host Scripts

Manual provisioning scripts for a disposable EC2 host that can run:

`flexagent serve sandbox-host`

The provisioning flow sets up:
- Ubuntu LTS instance (default from Canonical SSM parameter)
- extra gp3 EBS volume for ZFS pool
- `zfsutils-linux`
- `runsc` (gVisor runtime)
- ZFS datasets and initial base snapshot
- systemd unit for `flexagent-sandbox-host`
- optional local build/deploy of `flexagent`

## Files

- `provision.sh`: Create SG + EC2 + bootstrap + deploy/start service.
- `teardown.sh`: Terminate instance and delete SG.

## Quick Start

```bash
scripts/ec2-sandbox/provision.sh \
  --key-name my-ec2-key \
  --ssh-key-path ~/.ssh/my-ec2-key.pem
```

The script prints:
- instance ID
- public/private IPs
- sandbox-host RPC endpoint
- default base snapshot (`<pool>/bases/default@initial`)

If you need to skip local binary deployment:

```bash
scripts/ec2-sandbox/provision.sh \
  --key-name my-ec2-key \
  --ssh-key-path ~/.ssh/my-ec2-key.pem \
  --skip-binary-deploy
```

## Teardown

```bash
scripts/ec2-sandbox/teardown.sh
```

`teardown.sh` reads `.last_provision.env` by default and cleans up those resources.

## AWS Setup

### 1. Create an IAM user and attach policy

```bash
aws iam create-user --user-name flexagent-deployer

aws iam create-policy \
  --policy-name FlexAgentEC2Provisioning \
  --policy-document file://scripts/ec2-sandbox/iam-policy.json

aws iam attach-user-policy \
  --user-name flexagent-deployer \
  --policy-arn arn:aws:iam::<ACCOUNT_ID>:policy/FlexAgentEC2Provisioning
```

### 2. Create access key and configure profile

```bash
aws iam create-access-key --user-name flexagent-deployer
aws configure --profile flexagent
```

### 3. Run scripts with the profile

```bash
AWS_PROFILE=flexagent scripts/ec2-sandbox/provision.sh \
  --key-name my-ec2-key \
  --ssh-key-path ~/.ssh/my-ec2-key.pem
```

See `iam-policy.json` for the full policy. For more secure setups, consider AWS SSO (`aws configure sso`) or assume-role instead of long-lived access keys.

### Instance profile for SSM

Launched instances need **AmazonSSMManagedInstanceCore** so the SSM agent can receive commands. Create an instance profile with a role that has this policy, and pass it via `--iam-instance-profile`. The deployer policy allows `iam:PassRole` for roles matching `flexagent-sandbox-*`.

## Notes

- Defaults are intentionally simple for manual testing, not hardened production.
- RPC ingress defaults to `0.0.0.0/0`; restrict with `--rpc-cidr` for safer testing.
- SSH ingress defaults to your detected public IP `/32`; override with `--ssh-cidr`.
