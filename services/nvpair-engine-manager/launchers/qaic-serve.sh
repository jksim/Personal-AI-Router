#!/bin/sh
# SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
# SPDX-License-Identifier: Apache-2.0
#
# Runs Qualcomm's vLLM container for one QPC. Written by PAIR's installer.
# Usage: QAIC_HOST=<host> QAIC_PORT=<port> qaic-serve <model>
set -eu
model=$1
host=$QAIC_HOST
port=$QAIC_PORT
sysfs=${QAIC_SYSFS_ROOT:-/sys}
dev=${QAIC_DEV_ROOT:-/dev/accel}
image=ghcr.io/quic/cloud_ai_inference_vllm@sha256:55cbcb6a11a50a18613ac41523cfefcf75f7c1e64e5e0c43ff4a3334e33d97a9
fail() {
	echo "qaic-serve: $*" >&2
	exit 2
}
case "$model" in
/* | *..*) fail "refusing model id $model" ;;
esac

# vLLM does not exit on SIGTERM. Stop the container, which kills it after 30 s.
child=""
stop() {
	docker stop -t 30 nvpair-qaic >/dev/null 2>&1 || true
	if [ -n "$child" ]; then
		wait "$child" || true
	fi
	exit 0
}
trap stop TERM INT

qpc=""
for candidate in "$QAIC_MODELS_DIR/$model"/*/qpc-*/qpc; do
	if [ -f "$candidate/programqpc.bin" ]; then
		qpc=$candidate
		break
	fi
done
[ -n "$qpc" ] || fail "no QPC for $model under $QAIC_MODELS_DIR"
bundle=$(dirname "$qpc")
config=$(dirname "$bundle")
[ -f "$config/launch.args" ] || fail "$config/launch.args is missing"
launch=$(cat "$config/launch.args")

# A device-group index is a SoC's place in PCI address order. Mount only those
# SoCs' nodes, and number them from 0 inside the container.
group=$(printf '%s\n' "$launch" | sed -n 's/.*--device-group \([0-9,]*\).*/\1/p')
[ -n "$group" ] || fail "$config/launch.args has no --device-group"
socs=$(for node in "$sysfs"/class/accel/accel*; do
	printf '%s %s\n' "$(basename "$(readlink "$node/device")")" "$(basename "$node")"
done | sort)
devices=""
inner=""
count=0
for index in $(printf '%s\n' "$group" | tr ',' ' '); do
	node=$(printf '%s\n' "$socs" | sed -n "$((index + 1))p" | cut -d' ' -f2)
	[ -n "$node" ] || fail "SoC $index of --device-group $group is not present"
	devices="$devices --device $dev/$node:/dev/accel/$node"
	inner="$inner,$count"
	count=$((count + 1))
done
launch=$(printf '%s\n' "$launch" | sed "s/--device-group [0-9,]*/--device-group $(printf '%s' "$inner" | cut -c2-)/")
first=$(printf '%s\n' "$devices" | awk '{print $2}' | cut -d: -f1)
group_id=$(stat -c %g "$first")

docker rm -f nvpair-qaic >/dev/null 2>&1 || true
# shellcheck disable=SC2086
docker run --rm --init --name nvpair-qaic --user "$(id -u):$(id -g)" --group-add "$group_id" \
	--workdir /home/run $devices --network host --shm-size=4gb \
	-e HOME=/home/run -v "$QAIC_INSTALL_DIR/home:/home/run" \
	-e HF_HOME=/hf -v "$QAIC_INSTALL_DIR/hf:/hf" \
	-v "$bundle:/qpc:ro" -e VLLM_QAIC_QPC_PATH=/qpc/qpc \
	"$image" --model "$model" --host "$host" --port "$port" $launch &
child=$!
wait "$child"
