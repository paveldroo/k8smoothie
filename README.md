<h1 align="center">
  <br>
      🧋 k8smoothie
  <br>
</h1>
<h4 align="center">Deploy your k8s apps smoother with the 🧋 k8smoothie</h4>
<p align="center">
  <a href="https://pkg.go.dev/github.com/paveldroo/k8smoothie"><img src="https://pkg.go.dev/badge/github.com/paveldroo/k8smoothie.svg" alt="Go Reference"></a>
  <a href="https://goreportcard.com/report/github.com/paveldroo/k8smoothie"><img src="https://goreportcard.com/badge/github.com/paveldroo/k8smoothie" alt="Go Report Card"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/License-MIT-yellow.svg" alt="License: MIT"></a>
</p>
<br>

A lightweight CLI tool to **automate and unblock Kubernetes deployments** when running into **quota limits during long graceful shutdowns**.

### Problem

When your Kubernetes cluster has:
- **Limited instance resource quota**
- **Long graceful termination periods** (`terminationGracePeriodSeconds`)

Deployments can **get stuck** because:
- Old pods are still counted toward quota while terminating
- New ReplicaSet can't scale up
- Kubernetes retries but fails repeatedly
- Deployment becomes unresponsive until manual intervention

### Solution

This tool monitors deployments and ReplicaSets, detects when pods are fully terminated and quota becomes available, and automatically nudges the deployment to resume scheduling new pods.

### Features

- 📊 Monitors Deployment, ReplicaSet and Pod status
- 📦 Waits for every Deployment of a Helm release with one call and one overall timeout
- 🧠 Detects when deployment is stuck
- 🚀 Automatically "nudges" deployment to trigger new pod scheduling
- ⚙️ Designed for CI/CD usage — integrates seamlessly into pipelines to ensure reliable, automated rollouts without manual intervention
- 🖖 But you may use it manually
- 🔄 Tested against Helm and native Kubernetes Deployments

### CLI Usage

k8smoothie has two modes:

- **Explicit**: wait for the listed Deployments.
  ```bash
  k8smoothie -namespace=production -deployment=api,worker
  ```
- **Helm release**: wait for every Deployment in the namespace with annotation `meta.helm.sh/release-name: <release>` (set by Helm on every resource it manages). Exact match, so `endpointer` never picks up `endpointer-heavy`; Deployments whose `meta.helm.sh/release-namespace` names another namespace are skipped. Finding zero Deployments is an error.
  ```bash
  k8smoothie -namespace=production -helm-release=my-release -timeout=30m
  ```

All targets are watched concurrently under one overall `-timeout`. Both `-flag` and `--flag` work.

### Available Flags

| Flag | Description | Required | Default |
|------|-------------|----------|---------|
| -namespace | Namespace of the Deployments | ✅ Yes | — |
| -deployment | Comma-separated Deployment names (explicit mode) | one of `-deployment` / `-helm-release` | — |
| -helm-release | Helm release name; auto-discovers its Deployments. Mutually exclusive with `-deployment` | one of `-deployment` / `-helm-release` | — |
| -timeout | Overall deadline for all targets, Go duration (`90s`, `30m`, `1h`). `0` = no timeout | ❌ No | 0 |
| -frequency | Polling interval in seconds (1..3600) | ❌ No | 5 |
| -error-exit-code | Exit code (0..255) on rollout failure, timeout, cancel, kubectl or discovery error | ❌ No | 1 |
| -version | Print version and exit | ❌ No | — |

### How it works

For each Deployment, every `-frequency` seconds:

- **Done** when, like `kubectl rollout status`:
  - `status.observedGeneration >= metadata.generation`
  - `status.updatedReplicas == spec.replicas`
  - `status.replicas == status.updatedReplicas`
  - `status.availableReplicas == status.updatedReplicas`
- **Failed** when a pod of the current ReplicaSet has a container or init container waiting with `CrashLoopBackOff`, `ImagePullBackOff`, `InvalidImageName` or `CreateContainerConfigError`. Terminal pods (e.g. `Evicted`) and pods of old ReplicaSets are ignored. The same failure (pod, container and reason) must be seen on 2 checks in a row.
- **Failed** when the Deployment is paused (`spec.paused`) and not rolled out.
- **Kick** (annotate the Deployment with `last-activated=<time>`) when not done and the current ReplicaSet has fewer live pods than its `spec.replicas` (or does not exist yet) — including when there are zero pods, and regardless of other pods still terminating or pending, since the pod quota is shared by the whole namespace. This makes the controller retry creating pods right away instead of waiting out its backoff after quota errors. At most one kick per 15s; a failed kick (e.g. no `patch` permission) is logged and the wait continues. Pods that exist but are not yet Ready are waited for, not kicked.
- Otherwise **wait**.

ReplicaSets and pods are matched by `ownerReferences` uid, not by name. `ProgressDeadlineExceeded` is only logged as a warning; `-timeout` is the only deadline. Every kubectl call has a 30s request timeout. kubectl errors, including during discovery, are retried; a Deployment fails only after 3 consecutive errors. Intermediate errors and unconfirmed pod failures are not logged; only the final failure is.

On exit, a summary lists ✅ succeeded / 💥 failed / ⏰ timed out / 🛑 canceled per Deployment. SIGINT/SIGTERM (e.g. GitLab job cancel) cancels the wait and prints the summary.

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | All Deployments rolled out |
| `-error-exit-code` | Rollout failure, timeout, cancel, kubectl or discovery error |
| 2 | Usage error (bad or conflicting flags). Always 2, so CI misconfiguration is never hidden |

### GitLab CI example

Log-only policy: the job stays green even if the rollout does not finish, but usage errors still fail it.

```yaml
deploy:
  variables:
    NAMESPACE: my-namespace
    HELM_RELEASE: my-release
    K8SMOOTHIE_TIMEOUT: 30m
    K8SMOOTHIE_ERROR_EXIT_CODE: "0"
  script:
    - helm upgrade --install "$HELM_RELEASE" ./chart -n "$NAMESPACE"
    - k8smoothie -namespace="$NAMESPACE" -helm-release="$HELM_RELEASE" -timeout="$K8SMOOTHIE_TIMEOUT" -error-exit-code="$K8SMOOTHIE_ERROR_EXIT_CODE"
```

### Requirements

`kubectl` on `PATH`, configured for the target cluster, with `get` on deployments, replicasets, pods and `patch` on deployments in the namespace.

### Contributing
All project commands are managed using **Taskfile**, not `Makefile`.
For more information, see: [Taskfile Documentation](https://taskfile.dev/).

- `task test` — unit tests with the race detector
- `task lint` — `go vet` and golangci-lint
- `task build` — linux/amd64 binary, version from `git describe`
- `task minikube-start`, `task docker-build`, `task deploy`, then `task run` / `task run-release` — manual e2e on minikube

### License
MIT License - see [LICENSE](LICENSE) for full text.
