# AWS Setup for EC2 Sandbox Provisioning

## 1. Create an IAM user

Create a dedicated IAM user for programmatic access:

```bash
aws iam create-user --user-name flexagent-deployer
```

## 2. Attach the IAM policy

Create and attach the policy from `iam-policy.json`:

```bash
aws iam create-policy \
  --policy-name FlexAgentEC2Provisioning \
  --policy-document file://scripts/ec2-sandbox/iam-policy.json

aws iam attach-user-policy \
  --user-name flexagent-deployer \
  --policy-arn arn:aws:iam::<ACCOUNT_ID>:policy/FlexAgentEC2Provisioning
```

## 3. Create an access key pair

```bash
aws iam create-access-key --user-name flexagent-deployer
```

Save the `AccessKeyId` and `SecretAccessKey` from the output.

## 4. Configure a local AWS profile

```bash
aws configure --profile flexagent
```

Enter the access key ID, secret key, your preferred region (e.g., `us-east-1`),
and output format (`json`).

## 5. Run provisioning scripts

Set the profile when running scripts:

```bash
AWS_PROFILE=flexagent ./scripts/ec2-sandbox/provision.sh
```

## Alternatives for more secure setups

For production or team environments, consider:

- **AWS SSO / Identity Center** -- use `aws configure sso` to set up the
  `flexagent` profile via federated login instead of long-lived access keys.
- **Assume-role** -- create the policy on a role rather than a user, then
  assume it with `aws sts assume-role`. This limits credential lifetime and
  supports MFA enforcement.

## Instance profile for SSM

Launched sandbox instances need the **AmazonSSMManagedInstanceCore** managed
policy so the SSM agent can receive commands. Create an instance profile with
a role that has this policy attached, and pass it via `--iam-instance-profile`
when calling `ec2:RunInstances`. The `PassInstanceRole` statement in
`iam-policy.json` allows the deployer user to assign roles matching
`flexagent-sandbox-*`.
