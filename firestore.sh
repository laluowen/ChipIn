#!/usr/bin/env bash

HOST="127.0.0.1"
PORT="8219"
EMULATOR_HOST="${HOST}:${PORT}"
PID_FILE="/tmp/chipin-firestore-emulator.pid"
LOG_FILE="/tmp/chipin-firestore-emulator.log"

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
	printf 'Run this script with source so it can set FIRESTORE_EMULATOR_HOST in your shell.\n' >&2
	printf 'Example: source ./firestore.sh start\n' >&2
	exit 1
fi

start() {
	if nc -z "$HOST" "$PORT" >/dev/null 2>&1; then
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
