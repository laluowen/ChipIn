#!/usr/bin/env bash

# "localhost", not "127.0.0.1": on at least one dev machine (macOS), the
# Firestore client's gRPC connection hangs indefinitely against the literal
# IP but connects instantly against the hostname. CI (Linux) works fine with
# either, so this is a local-only quirk, not a correctness issue — but it's
# the kind of thing worth never debugging twice.
HOST="localhost"
PORT="8219"
EMULATOR_HOST="${HOST}:${PORT}"
PID_FILE="/tmp/chipin-firestore-emulator.pid"
LOG_FILE="/tmp/chipin-firestore-emulator.log"

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	printf 'Run this script with source so it can set FIRESTORE_EMULATOR_HOST in your shell.\n' >&2
	printf 'Example: source ./firestore.sh start\n' >&2
	exit 1
fi

# healthy does an actual HTTP round-trip rather than just checking the port
# accepts TCP connections: a wedged emulator process can keep its listening
# socket accepting at the kernel level long after the JVM itself has stopped
# responding, which a bare `nc -z` port check can't tell apart from healthy.
healthy() {
	curl -s -o /dev/null -m 2 "http://${EMULATOR_HOST}/"
}

start() {
	if healthy; then
		export FIRESTORE_EMULATOR_HOST="$EMULATOR_HOST"
		printf 'Firestore emulator already running on %s\n' "$FIRESTORE_EMULATOR_HOST"
		return 0
	fi

	if ! command -v gcloud >/dev/null 2>&1; then
		printf 'gcloud not found in PATH\n' >&2
		return 1
	fi

	gcloud emulators firestore start --host-port="$EMULATOR_HOST" >"$LOG_FILE" 2>&1 &
	echo $! >"$PID_FILE"
	export FIRESTORE_EMULATOR_HOST="$EMULATOR_HOST"
	printf 'Started Firestore emulator on %s\n' "$FIRESTORE_EMULATOR_HOST"
	printf 'Logs: %s\n' "$LOG_FILE"
}

stop() {
	if [[ -f "$PID_FILE" ]]; then
		kill "$(cat "$PID_FILE")" >/dev/null 2>&1 || true
		rm -f "$PID_FILE"
	fi
	unset FIRESTORE_EMULATOR_HOST
	printf 'Stopped Firestore emulator and unset FIRESTORE_EMULATOR_HOST\n'
}

case "${1:-}" in
	start)
		start
		;;
	stop)
		stop
		;;
	*)
		printf 'Usage: source ./firestore.sh {start|stop}\n' >&2
		return 1
		;;
	esac
