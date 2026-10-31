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
image=ghcr.io/quic/cloud_ai_inference_vllm@sha256:55cbcb6a11a50a18613ac41523cfefcf75f7c1e64e5e0c43ff4a3334e33d97a9
qpc=""
for candidate in "$QAIC_MODELS_DIR/$model"/*/qpc-*/qpc; do
	if [ -f "$candidate/programqpc.bin" ]; then
		qpc=$candidate
		break
	fi
done
if [ -z "$qpc" ]; then
	echo "qaic-serve: no QPC for $model under $QAIC_MODELS_DIR" >&2
	exit 2
fi
bundle=$(dirname "$qpc")
config=$(dirname "$bundle")
if [ ! -f "$config/launch.args" ]; then
	echo "qaic-serve: $config/launch.args is missing" >&2
	exit 2
fi
launch=$(cat "$config/launch.args")
devices=""
for node in /dev/accel/accel*; do
	devices="$devices --device $node"
done
group=$(stat -c %g /dev/accel/accel0)
docker rm -f nvpair-qaic >/dev/null 2>&1 || true
# shellcheck disable=SC2086
docker run --rm --init --name nvpair-qaic --user "$(id -u):$(id -g)" --group-add "$group" \
	--workdir /home/run $devices --network host --shm-size=4gb \
	-e HOME=/home/run -v "$QAIC_INSTALL_DIR/home:/home/run" \
	-e HF_HOME=/hf -v "$QAIC_INSTALL_DIR/hf:/hf" \
	-v "$bundle:/qpc:ro" -e VLLM_QAIC_QPC_PATH=/qpc/qpc \
	"$image" --model "$model" --host "$host" --port "$port" $launch &
child=$!
# vLLM does not exit on SIGTERM. Stop the container, which kills it after 30 s.
trap 'docker stop -t 30 nvpair-qaic >/dev/null 2>&1 || true; wait "$child" || true; exit 0' TERM INT
wait "$child"
