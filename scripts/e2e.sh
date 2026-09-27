#!/usr/bin/env bash
set -uo pipefail

NS=default
RELEASE=gracefulapp
MAIN=gracefulapp
HEAVY=gracefulapp-heavy
FREQ=${FREQ:-2}
ROLLOUT_TIMEOUT=${ROLLOUT_TIMEOUT:-15m}
WORKDIR=$(mktemp -d "${TMPDIR:-/tmp}/k8smoothie-e2e.XXXXXX")
BIN=$WORKDIR/k8smoothie
PASS=0
FAIL=0
FAILED=""
BG_PID=""

cleanup() {
	[ -n "$BG_PID" ] && kill "$BG_PID" 2>/dev/null
	printf '\nlogs: %s\n' "$WORKDIR"
}
trap cleanup EXIT

log() { printf '\n\033[1;34m== %s\033[0m\n' "$*"; }
pass() { PASS=$((PASS + 1)); printf '\033[32mPASS\033[0m %s\n' "$1"; }
fail() {
	FAIL=$((FAIL + 1))
	FAILED="$FAILED\n  - $1"
	printf '\033[31mFAIL\033[0m %s: %s\n' "$1" "$2"
	[ -f "${3:-}" ] && tail -n 20 "$3" | sed 's/^/    /'
}

k() { kubectl -n "$NS" "$@"; }

logfile() { printf '%s/%s.log' "$WORKDIR" "$(printf '%s' "$1" | tr -c 'a-zA-Z0-9' '_')"; }

# check NAME WANT_CODE GOT_CODE LOGFILE [PATTERN...]
check() {
	local name=$1 want=$2 got=$3 out=$4
	shift 4
	if [ "$got" != "$want" ]; then
		fail "$name" "exit $got, want $want" "$out"
		return
	fi
	local p
	for p in "$@"; do
		if ! grep -qF -- "$p" "$out"; then
			fail "$name" "missing '$p' in output" "$out"
			return
		fi
	done
	pass "$name"
}

# run_case NAME WANT_CODE [PATTERN...] -- K8SMOOTHIE_ARGS...
run_case() {
	local name=$1 want=$2
	shift 2
	local patterns=()
	while [ "$1" != "--" ]; do
		patterns+=("$1")
		shift
	done
	shift
	local out
	out=$(logfile "$name")
	"$BIN" "$@" >"$out" 2>&1
	check "$name" "$want" "$?" "$out" ${patterns[@]+"${patterns[@]}"}
}

bump() {
	local d
	for d in "$@"; do
		k patch deployment "$d" --type=merge \
			-p "{\"spec\":{\"template\":{\"metadata\":{\"labels\":{\"version\":\"e2e-$(date +%s)-$RANDOM\"}}}}}" >/dev/null
	done
}

settle() {
	local out
	out=$(logfile "settle-$*")
	if ! "$BIN" -namespace="$NS" -deployment="$(printf '%s' "$*" | tr ' ' ',')" -frequency="$FREQ" -timeout="$ROLLOUT_TIMEOUT" >"$out" 2>&1; then
		printf '\033[33mWARN\033[0m %s did not settle, see %s\n' "$*" "$out"
	fi
}

undo() {
	k rollout undo deployment/"$1" >/dev/null
	settle "$1"
}

preflight() {
	local c
	for c in kubectl minikube go docker task; do
		command -v "$c" >/dev/null || { echo "missing $c"; exit 1; }
	done
	minikube status >/dev/null || { echo "minikube is not running, run: task minikube-start"; exit 1; }
	[ -f go.mod ] && [ -d .deploy ] || { echo "run from the repo root"; exit 1; }
}

setup() {
	log "setup: image, binary, fresh deployments"
	task docker-build >/dev/null || exit 1
	go build -o "$BIN" . || exit 1
	kubectl config use-context minikube >/dev/null || exit 1
	k delete -f .deploy/deployment.yaml --ignore-not-found --wait=true >/dev/null
	task deploy >/dev/null || exit 1
	settle "$MAIN" "$HEAVY"
}

cases_usage() {
	log "usage errors always exit 2"
	run_case "usage: missing namespace" 2 "-namespace is required" -- -deployment=x -error-exit-code=0
	run_case "usage: both targets" 2 "mutually exclusive" -- -namespace="$NS" -deployment=x -helm-release=y -error-exit-code=0
	run_case "usage: flag injection in name" 2 "not a valid deployment name" -- -namespace="$NS" -deployment=-lapp=x -error-exit-code=0
	run_case "usage: exit code out of range" 2 "between 0 and 255" -- -namespace="$NS" -deployment=x -error-exit-code=300
	run_case "usage: bad timeout" 2 -- -namespace="$NS" -deployment=x -timeout=5 -error-exit-code=0
	run_case "version" 0 "k8smoothie" -- -version
}

cases_discovery() {
	log "discovery and missing objects"
	run_case "helm-release with no deployments" 1 "no deployments found" -- -namespace="$NS" -helm-release=e2e-nope
	run_case "helm-release exact match, heavy not picked by prefix" 1 "no deployments found" -- -namespace="$NS" -helm-release=gracefulapp-h
	run_case "missing deployment fails after 3 errors" 1 "2/3 consecutive errors" "💥 e2e-missing:" "NotFound" -- -namespace="$NS" -deployment=e2e-missing -frequency=1
}

cases_idle() {
	log "already rolled out"
	run_case "helm-release idle" 0 "✅ $MAIN:" "✅ $HEAVY:" -- -namespace="$NS" -helm-release="$RELEASE" -timeout=1m
	run_case "explicit idle, two names" 0 "✅ $MAIN:" "✅ $HEAVY:" -- -namespace="$NS" -deployment="$MAIN, $HEAVY" -timeout=1m
}

cases_rollout() {
	log "helm-release rollout of both deployments under pod quota (takes a few minutes)"
	bump "$MAIN" "$HEAVY"
	local name="helm-release rollout under quota"
	run_case "$name" 0 "✅ $MAIN:" "✅ $HEAVY:" "all deployments rolled out" -- \
		-namespace="$NS" -helm-release="$RELEASE" -frequency="$FREQ" -timeout="$ROLLOUT_TIMEOUT"
	printf '     kicks: %s\n' "$(grep -c '🥾' "$(logfile "$name")")"
	if k rollout status deployment/"$MAIN" --timeout=10s >/dev/null && k rollout status deployment/"$HEAVY" --timeout=10s >/dev/null; then
		pass "kubectl agrees rollout is complete"
	else
		fail "kubectl agrees rollout is complete" "kubectl rollout status not complete right after ✅"
	fi

	log "explicit mode (back-compat) single deployment rollout"
	bump "$MAIN"
	run_case "explicit rollout" 0 "✅ $MAIN:" -- -namespace="$NS" -deployment="$MAIN" -frequency="$FREQ" -timeout="$ROLLOUT_TIMEOUT"
}

cases_crashloop() {
	log "CrashLoopBackOff in $HEAVY"
	k patch deployment "$HEAVY" --type=json \
		-p '[{"op":"add","path":"/spec/template/spec/containers/0/env","value":[{"name":"CRASH","value":"1"}]}]' >/dev/null
	run_case "crashloop fails with error-exit-code" 1 "💥 $HEAVY:" "CrashLoopBackOff" "✅ $MAIN:" -- \
		-namespace="$NS" -helm-release="$RELEASE" -frequency="$FREQ" -timeout=5m
	run_case "crashloop log-only with error-exit-code=0" 0 "💥 $HEAVY:" "CrashLoopBackOff" -- \
		-namespace="$NS" -helm-release="$RELEASE" -frequency="$FREQ" -timeout=5m -error-exit-code=0
	bump "$MAIN"
	run_case "crashing prefix-sharing deployment does not affect $MAIN" 0 "✅ $MAIN:" -- \
		-namespace="$NS" -deployment="$MAIN" -frequency="$FREQ" -timeout="$ROLLOUT_TIMEOUT"
	undo "$HEAVY"
}

cases_imagepull() {
	log "ImagePullBackOff in $HEAVY"
	k patch deployment "$HEAVY" --type=json -p '[
		{"op":"replace","path":"/spec/template/spec/containers/0/image","value":"localhost:5999/e2e-nope:missing"},
		{"op":"replace","path":"/spec/template/spec/containers/0/imagePullPolicy","value":"IfNotPresent"}]' >/dev/null
	run_case "image pull backoff fails" 1 "💥 $HEAVY:" "ImagePullBackOff" -- \
		-namespace="$NS" -deployment="$HEAVY" -frequency="$FREQ" -timeout=5m
	undo "$HEAVY"
}

cases_timeout() {
	log "overall timeout"
	bump "$MAIN"
	run_case "timeout uses error-exit-code" 3 "⏰ $MAIN:" -- -namespace="$NS" -deployment="$MAIN" -frequency="$FREQ" -timeout=20s -error-exit-code=3
	settle "$MAIN"
}

cases_sigterm() {
	log "SIGTERM (GitLab cancel)"
	bump "$MAIN"
	local name="sigterm cancels and prints summary" out
	out=$(logfile "$name")
	"$BIN" -namespace="$NS" -deployment="$MAIN" -frequency="$FREQ" >"$out" 2>&1 &
	BG_PID=$!
	sleep 10
	kill -TERM "$BG_PID"
	wait "$BG_PID"
	local code=$?
	BG_PID=""
	check "$name" 1 "$code" "$out" "🛑 $MAIN:" "Summary"
	settle "$MAIN"
}

cases_paused() {
	log "paused deployment"
	k rollout pause deployment/"$MAIN" >/dev/null
	bump "$MAIN"
	run_case "paused deployment fails" 1 "💥 $MAIN:" "deployment is paused" -- -namespace="$NS" -deployment="$MAIN" -frequency=1 -timeout=2m
	k rollout resume deployment/"$MAIN" >/dev/null
	settle "$MAIN"
}

cases_zero() {
	log "zero replicas"
	k scale deployment "$HEAVY" --replicas=0 >/dev/null
	run_case "scaled to zero is done" 0 "✅ $HEAVY:" -- -namespace="$NS" -deployment="$HEAVY" -frequency="$FREQ" -timeout=5m
	k scale deployment "$HEAVY" --replicas=2 >/dev/null
	settle "$HEAVY"
}

preflight
setup
cases_usage
cases_discovery
cases_idle
cases_rollout
cases_crashloop
cases_imagepull
cases_timeout
cases_sigterm
cases_paused
cases_zero

log "result: $PASS passed, $FAIL failed"
if [ "$FAIL" -gt 0 ]; then
	printf "failed:$FAILED\n"
	exit 1
fi
