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

## Notes

- Defaults are intentionally simple for manual testing, not hardened production.
- RPC ingress defaults to `0.0.0.0/0`; restrict with `--rpc-cidr` for safer testing.
- SSH ingress defaults to your detected public IP `/32`; override with `--ssh-cidr`.
