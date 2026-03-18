#!/usr/bin/env bash
set -euo pipefail

usage() {
	cat <<'EOF'
Tear down EC2 resources created by scripts/ec2-sandbox/provision.sh.

Usage:
  scripts/ec2-sandbox/teardown.sh --flex-role <sandbox-host|orchestrator> [options]

Required:
  --flex-role                    Role to tear down: "sandbox-host" or "orchestrator".

Options:
  --region <region>              AWS region (default: from state file or aws config)
  --instance-id <id>             EC2 instance ID to terminate
  --security-group-id <id>       Security group ID to delete
  --state-file <path>            State file path (default: .last_provision_<role>.env)
  --keep-state-file              Keep the state file after teardown
  --help                         Show this help
EOF
}

log() {
	printf '[teardown] %s\n' "$*"
}

die() {
	printf '[teardown] ERROR: %s\n' "$*" >&2
	exit 1
}

require_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

state_value() {
	local key="$1"
	local file="$2"
	[[ -f "$file" ]] || return 0
	awk -F= -v k="$key" '$1 == k {print substr($0, index($0, "=") + 1); exit}' "$file"
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
KEEP_STATE_FILE=0
FLEX_ROLE=""
STATE_FILE=""

REGION=""
INSTANCE_ID=""
SECURITY_GROUP_ID=""

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
	--instance-id)
		INSTANCE_ID="$2"
		shift 2
		;;
	--security-group-id)
		SECURITY_GROUP_ID="$2"
		shift 2
		;;
	--state-file)
		STATE_FILE="$2"
		shift 2
		;;
	--keep-state-file)
		KEEP_STATE_FILE=1
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

[[ -n "$FLEX_ROLE" ]] || die "--flex-role is required (sandbox-host or orchestrator)"
[[ "$FLEX_ROLE" == "sandbox-host" || "$FLEX_ROLE" == "orchestrator" ]] || die "--flex-role must be 'sandbox-host' or 'orchestrator'"

if [[ -z "$STATE_FILE" ]]; then
	STATE_FILE="$ROOT_DIR/scripts/ec2-sandbox/.last_provision_${FLEX_ROLE}.env"
fi

if [[ -z "$REGION" ]]; then
	REGION="$(state_value REGION "$STATE_FILE")"
fi
if [[ -z "$INSTANCE_ID" ]]; then
	INSTANCE_ID="$(state_value INSTANCE_ID "$STATE_FILE")"
fi
if [[ -z "$SECURITY_GROUP_ID" ]]; then
	SECURITY_GROUP_ID="$(state_value SECURITY_GROUP_ID "$STATE_FILE")"
fi

if [[ -z "$REGION" ]]; then
	REGION="$(aws configure get region 2>/dev/null || true)"
fi
REGION="${REGION:-us-east-1}"

[[ -n "$INSTANCE_ID" ]] || die "missing instance id (pass --instance-id or provide a state file)"

log "tearing down ${FLEX_ROLE}: instance=$INSTANCE_ID (region=$REGION)"
aws ec2 terminate-instances \
	--region "$REGION" \
	--instance-ids "$INSTANCE_ID" \
	--query 'TerminatingInstances[0].CurrentState.Name' \
	--output text >/dev/null

aws ec2 wait instance-terminated --region "$REGION" --instance-ids "$INSTANCE_ID"
log "instance terminated"

if [[ -n "$SECURITY_GROUP_ID" ]]; then
	log "deleting security group: $SECURITY_GROUP_ID"
	if ! aws ec2 delete-security-group --region "$REGION" --group-id "$SECURITY_GROUP_ID" >/dev/null 2>&1; then
		log "security group delete failed (possibly already deleted or still in use)"
	else
		log "security group deleted"
	fi
fi

if [[ "$KEEP_STATE_FILE" -eq 0 && -f "$STATE_FILE" ]]; then
	rm -f "$STATE_FILE"
	log "removed state file: $STATE_FILE"
fi

log "teardown complete (${FLEX_ROLE})"
