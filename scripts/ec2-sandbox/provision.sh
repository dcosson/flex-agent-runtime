#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat <<'EOF'
Provision an EC2 instance for flexagent.

Usage:
  scripts/ec2-sandbox/provision.sh \
    --flex-role <sandbox-host|orchestrator> \
    --key-name <ec2-keypair-name> \
    --ssh-key-path <path-to-private-key.pem> \
    [options]

Required:
  --flex-role             Role: "sandbox-host" or "orchestrator".
  --key-name              Existing EC2 key pair name for SSH.
  --ssh-key-path          Local path to the private key for the key pair.

Common options (all roles):
  --region <region>             AWS region (default: aws configure get region).
  --instance-type <type>        Instance type (default: t4g.large for sandbox-host, t4g.small for orchestrator).
  --ssh-cidr <cidr>             CIDR allowed for SSH ingress (default: caller_ip/32).
  --rpc-cidr <cidr>             CIDR allowed for RPC ingress (default: 0.0.0.0/0).
  --rpc-port <port>             RPC port (default: 8080).
  --subnet-id <subnet>          Subnet to launch into (default: first default subnet).
  --ami-id <ami>                Explicit AMI ID (default: latest Ubuntu LTS via SSM).
  --name-prefix <prefix>        Name prefix for resources (default: flexagent-<role>).
  --skip-binary-deploy          Do not build/copy flexagent; print manual commands.
  --help                        Show this help.

Orchestrator options (only with --flex-role orchestrator):
  --sandbox-host-addr <host:port>  Sandbox-host private IP and port.
  --direct-host-addr <host:port>   Pre-existing agent host for direct mode (skips EC2 provisioning).

Sandbox-host options (only with --flex-role sandbox-host):
  --volume-size-gb <gb>         Extra EBS size for ZFS pool (default: 80).
  --pool-name <name>            ZFS pool name (default: tank).
  --default-quota-gb <gb>       Per-session default ZFS quota in GB (default: 20).

Notes:
  - Assumes AWS CLI credentials are already configured.
  - Launches Ubuntu arm64 (Graviton).
  - sandbox-host: installs zfsutils-linux, runsc, creates ZFS pool/datasets.
  - orchestrator: minimal setup, installs flexagent binary only.
EOF
}

log() {
	printf '[provision] %s\n' "$*"
}

die() {
	printf '[provision] ERROR: %s\n' "$*" >&2
	exit 1
}

require_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

caller_public_ip() {
	curl -fsSL https://checkip.amazonaws.com 2>/dev/null | tr -d '[:space:]' || true
}

resolve_ubuntu_ami() {
	local region="$1"
	local release
	local parameter
	local ami

	for release in 24.04 22.04; do
		parameter="/aws/service/canonical/ubuntu/server/${release}/stable/current/arm64/hvm/ebs-gp3/ami-id"
		ami="$(aws ssm get-parameter \
			--region "$region" \
			--name "$parameter" \
			--query 'Parameter.Value' \
			--output text 2>/dev/null || true)"
		if [[ -n "$ami" && "$ami" != "None" ]]; then
			echo "$ami"
			return 0
		fi
	done
	return 1
}

wait_for_ssh() {
	local key_path="$1"
	local host="$2"
	local user="${3:-ubuntu}"
	local max_attempts=60
	local attempt
	for ((attempt = 1; attempt <= max_attempts; attempt++)); do
		if ssh -i "$key_path" \
			-o BatchMode=yes \
			-o StrictHostKeyChecking=no \
			-o UserKnownHostsFile=/dev/null \
			-o ConnectTimeout=5 \
			"${user}@${host}" 'echo ok' >/dev/null 2>&1; then
			return 0
		fi
		sleep 5
	done
	return 1
}

# --- Generate user-data scripts per role ---

generate_sandbox_host_userdata() {
	cat <<EOF
#!/usr/bin/env bash
set -euxo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl jq unzip zfsutils-linux

modprobe zfs || true

arch="\$(uname -m)"
case "\$arch" in
  x86_64|amd64) runsc_arch="x86_64" ;;
  aarch64|arm64) runsc_arch="aarch64" ;;
  *) echo "unsupported architecture for runsc: \$arch" >&2; exit 1 ;;
esac

runsc_version="\${RUNSC_VERSION:-release/latest}"
curl -fsSL "https://storage.googleapis.com/gvisor/releases/\${runsc_version}/\${runsc_arch}/runsc" -o /usr/local/bin/runsc
chmod +x /usr/local/bin/runsc
/usr/local/bin/runsc --version || true

mkdir -p /var/lib/runsc /var/lib/flexagent/bundles /var/lib/flexagent/bases /var/lib/flexagent/sessions

# Set a standard TERM so SSH sessions from modern terminals (Ghostty, Kitty)
# don't trigger "terminal is not fully functional" warnings due to missing terminfo.
# Also disable the systemctl pager to avoid pagination in non-interactive sessions.
cat > /etc/profile.d/term-compat.sh <<'PROFILE'
export TERM=xterm-256color
export SYSTEMD_PAGER=
PROFILE

if [[ ! -f /sys/fs/cgroup/cgroup.controllers ]]; then
  echo "warning: cgroup v2 controllers file not found; gVisor resource controls may be degraded" >&2
fi

root_src="\$(findmnt -n -o SOURCE /)"
root_disk="\$(lsblk -no pkname "\$root_src" | head -n1 || true)"
data_dev=""
for dev in /dev/nvme*n1 /dev/xvd[b-z] /dev/sd[b-z]; do
  [[ -b "\$dev" ]] || continue
  name="\$(basename "\$dev")"
  if [[ -n "\$root_disk" && "\$name" == "\$root_disk" ]]; then
    continue
  fi
  data_dev="\$dev"
  break
done

if [[ -z "\$data_dev" ]]; then
  echo "unable to locate non-root data device for zpool" >&2
  exit 1
fi

if ! zpool list "${POOL_NAME}" >/dev/null 2>&1; then
  zpool create -f "${POOL_NAME}" "\$data_dev"
fi

zfs list "${BASES_DATASET}" >/dev/null 2>&1 || zfs create -o mountpoint=/var/lib/flexagent/bases "${BASES_DATASET}"
zfs list "${SESSIONS_DATASET}" >/dev/null 2>&1 || zfs create -o mountpoint=/var/lib/flexagent/sessions "${SESSIONS_DATASET}"

default_base="${BASES_DATASET}/default"
zfs list "\$default_base" >/dev/null 2>&1 || zfs create -o mountpoint=/var/lib/flexagent/bases/default "\$default_base"
echo "flexagent sandbox base image" >/var/lib/flexagent/bases/default/README.txt
zfs list -t snapshot "\${default_base}@initial" >/dev/null 2>&1 || zfs snapshot "\${default_base}@initial"

imds_token="\$(curl -fsSL -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token || true)"
private_ip="\$(curl -fsSL -H "X-aws-ec2-metadata-token: \${imds_token}" http://169.254.169.254/latest/meta-data/local-ipv4 || true)"

cat >/etc/default/flexagent-sandbox-host <<ENVVARS
SANDBOX_HOST_LISTEN=:${RPC_PORT}
SANDBOX_STORAGE_BACKEND=zfs
SANDBOX_CONTAINER_RUNTIME=gvisor
SANDBOX_POOL_NAME=${POOL_NAME}
SANDBOX_BASES_DATASET=${BASES_DATASET}
SANDBOX_SESSIONS_DATASET=${SESSIONS_DATASET}
SANDBOX_DEFAULT_QUOTA=${DEFAULT_QUOTA_BYTES}
SANDBOX_RUNSC_PATH=/usr/local/bin/runsc
SANDBOX_RUNSC_ROOT=/var/lib/runsc
SANDBOX_BUNDLE_BASE_DIR=/var/lib/flexagent/bundles
SANDBOX_API_VERSION=v1
SANDBOX_MIN_API_VERSION=v1
SANDBOX_ADVERTISE_ADDR=\${private_ip}
ENVVARS

cat >/etc/systemd/system/flexagent-sandbox-host.service <<UNIT
[Unit]
Description=flexagent sandbox-host
After=network-online.target cloud-final.service
Wants=network-online.target
ConditionPathExists=/usr/local/bin/flexagent

[Service]
Type=simple
EnvironmentFile=/etc/default/flexagent-sandbox-host
ExecStart=/usr/local/bin/flexagent serve sandbox-host
Restart=on-failure
RestartSec=2
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
touch /var/lib/flexagent/bootstrap-complete
EOF
}

generate_orchestrator_userdata() {
	cat <<EOF
#!/usr/bin/env bash
set -euxo pipefail

export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl jq unzip git

mkdir -p /var/lib/flexagent

# Set a standard TERM so SSH sessions from modern terminals (Ghostty, Kitty)
# don't trigger "terminal is not fully functional" warnings due to missing terminfo.
# Also disable the systemctl pager to avoid pagination in non-interactive sessions.
cat > /etc/profile.d/term-compat.sh <<'PROFILE'
export TERM=xterm-256color
export SYSTEMD_PAGER=
PROFILE

imds_token="\$(curl -fsSL -X PUT -H 'X-aws-ec2-metadata-token-ttl-seconds: 60' http://169.254.169.254/latest/api/token || true)"
private_ip="\$(curl -fsSL -H "X-aws-ec2-metadata-token: \${imds_token}" http://169.254.169.254/latest/meta-data/local-ipv4 || true)"

cat >/etc/default/flexagent-orchestrator <<ENVVARS
FLEXAGENT_LISTEN=:${RPC_PORT}
ORCHESTRATOR_SANDBOX_HOST_ADDR=${SANDBOX_HOST_ADDR}
ORCHESTRATOR_DIRECT_HOST_ADDR=${DIRECT_HOST_ADDR}
ENVVARS

cat >/etc/systemd/system/flexagent-orchestrator.service <<UNIT
[Unit]
Description=flexagent orchestrator
After=network-online.target cloud-final.service
Wants=network-online.target
ConditionPathExists=/usr/local/bin/flexagent

[Service]
Type=simple
EnvironmentFile=/etc/default/flexagent-orchestrator
ExecStart=/usr/local/bin/flexagent serve orchestrator
Restart=on-failure
RestartSec=2
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
touch /var/lib/flexagent/bootstrap-complete
EOF
}

# --- Main ---

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STATE_DIR="$ROOT_DIR/scripts/ec2-sandbox"

REGION="$(aws configure get region 2>/dev/null || true)"
REGION="${REGION:-us-east-1}"
FLEX_ROLE=""
INSTANCE_TYPE=""
VOLUME_SIZE_GB="80"
RPC_PORT="8080"
SSH_CIDR=""
RPC_CIDR="0.0.0.0/0"
SUBNET_ID=""
AMI_ID=""
NAME_PREFIX=""
POOL_NAME="tank"
DEFAULT_QUOTA_GB="20"
KEY_NAME=""
SSH_KEY_PATH=""
SANDBOX_HOST_ADDR=""
DIRECT_HOST_ADDR=""
SKIP_BINARY_DEPLOY=0

while [[ $# -gt 0 ]]; do
	case "$1" in
	--flex-role)
		FLEX_ROLE="$2"
		shift 2
		;;
	--region)
		REGION="$2"
		shift 2
		;;
	--instance-type)
		INSTANCE_TYPE="$2"
		shift 2
		;;
	--volume-size-gb)
		VOLUME_SIZE_GB="$2"
		shift 2
		;;
	--rpc-port)
		RPC_PORT="$2"
		shift 2
		;;
	--ssh-cidr)
		SSH_CIDR="$2"
		shift 2
		;;
	--rpc-cidr)
		RPC_CIDR="$2"
		shift 2
		;;
	--subnet-id)
		SUBNET_ID="$2"
		shift 2
		;;
	--ami-id)
		AMI_ID="$2"
		shift 2
		;;
	--name-prefix)
		NAME_PREFIX="$2"
		shift 2
		;;
	--pool-name)
		POOL_NAME="$2"
		shift 2
		;;
	--default-quota-gb)
		DEFAULT_QUOTA_GB="$2"
		shift 2
		;;
	--key-name)
		KEY_NAME="$2"
		shift 2
		;;
	--ssh-key-path)
		SSH_KEY_PATH="$2"
		shift 2
		;;
	--sandbox-host-addr)
		SANDBOX_HOST_ADDR="$2"
		shift 2
		;;
	--direct-host-addr)
		DIRECT_HOST_ADDR="$2"
		shift 2
		;;
	--skip-binary-deploy)
		SKIP_BINARY_DEPLOY=1
		shift
		;;
	--help|-h)
		usage
		exit 0
		;;
	*)
		die "unknown argument: $1"
		;;
	esac
done

require_cmd aws
require_cmd jq
require_cmd ssh
require_cmd scp
require_cmd curl

[[ -n "$FLEX_ROLE" ]] || die "--flex-role is required (sandbox-host or orchestrator)"
[[ "$FLEX_ROLE" == "sandbox-host" || "$FLEX_ROLE" == "orchestrator" ]] || die "--flex-role must be 'sandbox-host' or 'orchestrator'"
[[ -n "$KEY_NAME" ]] || die "--key-name is required"
[[ -n "$SSH_KEY_PATH" ]] || die "--ssh-key-path is required"
[[ -f "$SSH_KEY_PATH" ]] || die "ssh key not found: $SSH_KEY_PATH"
if [[ "$FLEX_ROLE" == "orchestrator" && -z "$SANDBOX_HOST_ADDR" && -z "$DIRECT_HOST_ADDR" ]]; then
	die "orchestrator requires --sandbox-host-addr and/or --direct-host-addr"
fi

# Role-specific defaults
if [[ -z "$NAME_PREFIX" ]]; then
	NAME_PREFIX="flexagent-${FLEX_ROLE}"
fi
if [[ -z "$INSTANCE_TYPE" ]]; then
	if [[ "$FLEX_ROLE" == "orchestrator" ]]; then
		INSTANCE_TYPE="t4g.small"
	else
		INSTANCE_TYPE="t4g.large"
	fi
fi
STATE_FILE="${STATE_DIR}/.last_provision_${FLEX_ROLE}.env"

if [[ -z "$SSH_CIDR" ]]; then
	ip="$(caller_public_ip)"
	if [[ -n "$ip" ]]; then
		SSH_CIDR="${ip}/32"
	else
		die "unable to detect caller public IP; pass --ssh-cidr explicitly"
	fi
fi

if [[ -z "$SUBNET_ID" ]]; then
	SUBNET_ID="$(aws ec2 describe-subnets \
		--region "$REGION" \
		--filters Name=default-for-az,Values=true \
		--query 'Subnets[0].SubnetId' \
		--output text)"
fi
[[ -n "$SUBNET_ID" && "$SUBNET_ID" != "None" ]] || die "could not resolve subnet-id"

VPC_ID="$(aws ec2 describe-subnets \
	--region "$REGION" \
	--subnet-ids "$SUBNET_ID" \
	--query 'Subnets[0].VpcId' \
	--output text)"
[[ -n "$VPC_ID" && "$VPC_ID" != "None" ]] || die "could not resolve VPC for subnet $SUBNET_ID"

if [[ -z "$AMI_ID" ]]; then
	AMI_ID="$(resolve_ubuntu_ami "$REGION")" || die "failed to resolve Ubuntu AMI in region $REGION"
fi

timestamp="$(date +%Y%m%d-%H%M%S)"
SG_NAME="${NAME_PREFIX}-sg-${timestamp}"
INSTANCE_NAME="${NAME_PREFIX}-${timestamp}"

log "role=$FLEX_ROLE region=$REGION subnet=$SUBNET_ID ami=$AMI_ID instance_type=$INSTANCE_TYPE"
log "creating security group: $SG_NAME"
SG_ID="$(aws ec2 create-security-group \
	--region "$REGION" \
	--group-name "$SG_NAME" \
	--description "flexagent ${FLEX_ROLE} sg (${timestamp})" \
	--vpc-id "$VPC_ID" \
	--query GroupId \
	--output text)"

aws ec2 create-tags \
	--region "$REGION" \
	--resources "$SG_ID" \
	--tags "Key=Name,Value=${SG_NAME}" "Key=Project,Value=flexagent-runtime" "Key=FlexRole,Value=${FLEX_ROLE}" >/dev/null

aws ec2 authorize-security-group-ingress \
	--region "$REGION" \
	--group-id "$SG_ID" \
	--ip-permissions "IpProtocol=tcp,FromPort=22,ToPort=22,IpRanges=[{CidrIp=${SSH_CIDR},Description=ssh}]" >/dev/null

aws ec2 authorize-security-group-ingress \
	--region "$REGION" \
	--group-id "$SG_ID" \
	--ip-permissions "IpProtocol=tcp,FromPort=${RPC_PORT},ToPort=${RPC_PORT},IpRanges=[{CidrIp=${RPC_CIDR},Description=rpc}]" >/dev/null

# --- Generate user-data and block device mappings per role ---

user_data_file="$(mktemp)"
BLOCK_DEVICE_MAPPINGS=""

if [[ "$FLEX_ROLE" == "sandbox-host" ]]; then
	DEFAULT_QUOTA_BYTES="$((DEFAULT_QUOTA_GB * 1024 * 1024 * 1024))"
	BASES_DATASET="${POOL_NAME}/bases"
	SESSIONS_DATASET="${POOL_NAME}/sessions"
	generate_sandbox_host_userdata >"$user_data_file"
	BLOCK_DEVICE_MAPPINGS="--block-device-mappings [{\"DeviceName\":\"/dev/sdf\",\"Ebs\":{\"VolumeType\":\"gp3\",\"VolumeSize\":${VOLUME_SIZE_GB},\"DeleteOnTermination\":true}}]"
else
	generate_orchestrator_userdata >"$user_data_file"
fi

# shellcheck disable=SC2086
instance_id="$(aws ec2 run-instances \
	--region "$REGION" \
	--image-id "$AMI_ID" \
	--instance-type "$INSTANCE_TYPE" \
	--key-name "$KEY_NAME" \
	--security-group-ids "$SG_ID" \
	--subnet-id "$SUBNET_ID" \
	$BLOCK_DEVICE_MAPPINGS \
	--tag-specifications "ResourceType=instance,Tags=[{Key=Name,Value=${INSTANCE_NAME}},{Key=Project,Value=flexagent-runtime},{Key=FlexRole,Value=${FLEX_ROLE}}]" \
	--user-data "file://${user_data_file}" \
	--query 'Instances[0].InstanceId' \
	--output text)"

rm -f "$user_data_file"

[[ -n "$instance_id" && "$instance_id" != "None" ]] || die "failed to create instance"
log "instance created: $instance_id"

aws ec2 wait instance-running --region "$REGION" --instance-ids "$instance_id"
aws ec2 wait instance-status-ok --region "$REGION" --instance-ids "$instance_id"

public_ip="$(aws ec2 describe-instances \
	--region "$REGION" \
	--instance-ids "$instance_id" \
	--query 'Reservations[0].Instances[0].PublicIpAddress' \
	--output text)"
private_ip="$(aws ec2 describe-instances \
	--region "$REGION" \
	--instance-ids "$instance_id" \
	--query 'Reservations[0].Instances[0].PrivateIpAddress' \
	--output text)"

[[ -n "$public_ip" && "$public_ip" != "None" ]] || die "instance has no public IP; use a public subnet or add access via bastion"
[[ -n "$private_ip" && "$private_ip" != "None" ]] || die "failed to resolve private IP"

log "waiting for ssh on ${public_ip}"
wait_for_ssh "$SSH_KEY_PATH" "$public_ip" "ubuntu" || die "ssh did not become ready"
ssh -i "$SSH_KEY_PATH" \
	-o StrictHostKeyChecking=no \
	-o UserKnownHostsFile=/dev/null \
	"ubuntu@${public_ip}" \
	'sudo cloud-init status --wait || true'

# --- Deploy binary and start service ---

SERVICE_NAME="flexagent-${FLEX_ROLE}"
if [[ "$FLEX_ROLE" == "orchestrator" ]]; then
	SERVICE_NAME="flexagent-orchestrator"
else
	SERVICE_NAME="flexagent-sandbox-host"
fi

if [[ "$SKIP_BINARY_DEPLOY" -eq 0 ]]; then
	tmp_bin="$(mktemp)"
	log "building flexagent binary for linux/arm64"
	(
		cd "$ROOT_DIR"
		GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o "$tmp_bin" ./cmd/flexagent
	)

	log "copying binary to instance"
	scp -i "$SSH_KEY_PATH" \
		-o StrictHostKeyChecking=no \
		-o UserKnownHostsFile=/dev/null \
		"$tmp_bin" "ubuntu@${public_ip}:/tmp/flexagent"
	rm -f "$tmp_bin"

	log "installing and starting ${SERVICE_NAME} service"
	ssh -i "$SSH_KEY_PATH" \
		-o StrictHostKeyChecking=no \
		-o UserKnownHostsFile=/dev/null \
		"ubuntu@${public_ip}" \
		"sudo install -m 0755 /tmp/flexagent /usr/local/bin/flexagent && \
         sudo systemctl daemon-reload && \
         sudo systemctl enable --now ${SERVICE_NAME} && \
         sudo systemctl --no-pager --full status ${SERVICE_NAME} | tail -n 40"
else
	log "skipping binary deploy (--skip-binary-deploy set)"
fi

# --- Write state file and summary ---

mkdir -p "$(dirname "$STATE_FILE")"
cat >"$STATE_FILE" <<EOF
FLEX_ROLE=${FLEX_ROLE}
REGION=${REGION}
INSTANCE_ID=${instance_id}
SECURITY_GROUP_ID=${SG_ID}
PUBLIC_IP=${public_ip}
PRIVATE_IP=${private_ip}
RPC_PORT=${RPC_PORT}
EOF

if [[ "$FLEX_ROLE" == "sandbox-host" ]]; then
	cat >>"$STATE_FILE" <<EOF
POOL_NAME=${POOL_NAME}
BASES_DATASET=${BASES_DATASET}
SESSIONS_DATASET=${SESSIONS_DATASET}
BASE_SNAPSHOT=${BASES_DATASET}/default@initial
EOF
fi

cat <<EOF

Provisioning complete (${FLEX_ROLE}).

State file:
  ${STATE_FILE}

Connection info:
  Instance ID:    ${instance_id}
  Region:         ${REGION}
  Role:           ${FLEX_ROLE}
  Public IP:      ${public_ip}
  Private IP:     ${private_ip}
  RPC Endpoint:   http://${public_ip}:${RPC_PORT}
EOF

if [[ "$FLEX_ROLE" == "sandbox-host" ]]; then
	cat <<EOF

Sandbox config:
  Pool:           ${POOL_NAME}
  Bases dataset:  ${BASES_DATASET}
  Sessions ds:    ${SESSIONS_DATASET}
  Base snapshot:  ${BASES_DATASET}/default@initial

  ┌─────────────────────────────────────────────────────┐
  │  SANDBOX_HOST_ADDR=${private_ip}:${RPC_PORT}        │
  │  Use this when configuring the orchestrator.        │
  └─────────────────────────────────────────────────────┘
EOF
fi

cat <<EOF

If you used --skip-binary-deploy, run:
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o /tmp/flexagent ./cmd/flexagent
  scp -i ${SSH_KEY_PATH} /tmp/flexagent ubuntu@${public_ip}:/tmp/flexagent
  ssh -i ${SSH_KEY_PATH} ubuntu@${public_ip} 'sudo install -m 0755 /tmp/flexagent /usr/local/bin/flexagent && sudo systemctl daemon-reload && sudo systemctl enable --now ${SERVICE_NAME}'

Teardown:
  scripts/ec2-sandbox/teardown.sh --flex-role ${FLEX_ROLE} --region ${REGION} --instance-id ${instance_id} --security-group-id ${SG_ID}
EOF
