#!/bin/bash
set -euo pipefail

POOL_NAME="${SANDBOX_POOL_NAME:-testpool}"
POOL_DIR="/var/lib/zfs-test"
POOL_IMAGE="$POOL_DIR/pool.img"
POOL_SIZE="2G"

echo "==> Initializing ZFS test pool: $POOL_NAME"

# Check if pool already exists
if zpool list "$POOL_NAME" &>/dev/null; then
    echo "==> Pool $POOL_NAME already exists, skipping creation"
else
    echo "==> Creating pool image at $POOL_IMAGE ($POOL_SIZE)"
    mkdir -p "$POOL_DIR"
    truncate -s "$POOL_SIZE" "$POOL_IMAGE"

    echo "==> Creating ZFS pool"
    zpool create -f "$POOL_NAME" "$POOL_IMAGE"
fi

# Create datasets if they don't exist
for ds in bases sessions; do
    if ! zfs list "$POOL_NAME/$ds" &>/dev/null; then
        echo "==> Creating dataset: $POOL_NAME/$ds"
        zfs create "$POOL_NAME/$ds"
    fi
done

# Create base snapshot if it doesn't exist
BASE_SNAPSHOT="$POOL_NAME/bases/ubuntu-base@ready"
if ! zfs list -t snapshot "$BASE_SNAPSHOT" &>/dev/null; then
    if ! zfs list "$POOL_NAME/bases/ubuntu-base" &>/dev/null; then
        echo "==> Creating base dataset: $POOL_NAME/bases/ubuntu-base"
        zfs create "$POOL_NAME/bases/ubuntu-base"
    fi

    # Seed with minimal test files
    MP=$(zfs get -H -o value mountpoint "$POOL_NAME/bases/ubuntu-base")
    echo "hello world" > "$MP/input.txt"
    mkdir -p "$MP/src"
    echo 'package main; import "fmt"; func main() { fmt.Println("hello") }' > "$MP/src/main.go"

    echo "==> Creating base snapshot: $BASE_SNAPSHOT"
    zfs snapshot "$BASE_SNAPSHOT"
fi

echo "==> ZFS test pool ready"
zpool status "$POOL_NAME"
zfs list -r "$POOL_NAME"
