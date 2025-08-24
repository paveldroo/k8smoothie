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

A lightweight library to **automate and unblock Kubernetes deployments** when running into **quota limits during long graceful shutdowns**.

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

This library monitors deployments and ReplicaSets, detects when pods are fully terminated and quota becomes available, and automatically nudges the deployment to resume scheduling new pods.

### Features

- 📊 Monitors Pod and ReplicaSet status
- 🧠 Detects when deployment is stuck
- 🚀 Automatically "nudges" deployment to trigger new pod scheduling
- ⚙️ Designed for CI/CD usage — integrates seamlessly into pipelines to ensure reliable, automated rollouts without manual intervention
- 🖖 But you may use it manually
- 🔄 Tested against Helm and native Kubernetes Deployments

### CLI Usage with Flags

You can also use the library manually via CLI by passing the following flags:
```bash
k8smoothie -namespace= -deployment= -error-exit-code=
```

### Available Flags:

| Flag | Description | Required | Default |
|------|-------------|----------|---------|
| -namespace | The namespace of the deployment to monitor and nudge | ✅ Yes | — |
| -deployment | The name of the deployment to target | ✅ Yes | — |
| -error-exit-code | Exit code to return on error for CI usage | ❌ No | 1 |

### Example:
```bash
k8smoothie -namespace=production -deployment=my-app-deployment
```

Or with a custom exit code for silent fail in CI:
```bash
k8smoothie -namespace=staging -deployment=api-server -error-exit-code=0
```

### Contributing
All project commands are managed using **Taskfile**, not `Makefile`.
For more information, see: [Taskfile Documentation](https://taskfile.dev/).

### License
MIT License - see [LICENSE](LICENSE) for full text.
