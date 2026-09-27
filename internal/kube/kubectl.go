package kube

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

const waitDelay = 5 * time.Second

var (
	dns1123Label     = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	dns1123Subdomain = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

// ValidNamespace reports whether s is a valid Kubernetes namespace name.
func ValidNamespace(s string) bool {
	return len(s) <= 63 && dns1123Label.MatchString(s)
}

// ValidName reports whether s is a valid Kubernetes object name.
func ValidName(s string) bool {
	return len(s) <= 253 && dns1123Subdomain.MatchString(s)
}

// Client is the subset of the Kubernetes API used by k8smoothie.
type Client interface {
	GetDeployment(ctx context.Context, ns, name string) (Deployment, error)
	ListDeployments(ctx context.Context, ns string) ([]Deployment, error)
	ListReplicaSets(ctx context.Context, ns string, selector map[string]string) ([]ReplicaSet, error)
	ListPods(ctx context.Context, ns string, selector map[string]string) ([]Pod, error)
	Annotate(ctx context.Context, ns, deployment, key, value string) error
}

// Kubectl implements Client by shelling out to kubectl.
type Kubectl struct {
	Bin string
}

func (k Kubectl) GetDeployment(ctx context.Context, ns, name string) (Deployment, error) {
	var d Deployment
	if err := k.getJSON(ctx, &d, "-n", ns, "get", "deployment", name, "-o", "json"); err != nil {
		return Deployment{}, err
	}
	if d.Kind != "Deployment" || d.Metadata.Name != name {
		return Deployment{}, fmt.Errorf("kubectl get deployment %s: unexpected object kind=%q name=%q", name, d.Kind, d.Metadata.Name)
	}
	return d, nil
}

func (k Kubectl) ListDeployments(ctx context.Context, ns string) ([]Deployment, error) {
	var l DeploymentList
	if err := k.getJSON(ctx, &l, "-n", ns, "get", "deployments", "-o", "json"); err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (k Kubectl) ListReplicaSets(ctx context.Context, ns string, selector map[string]string) ([]ReplicaSet, error) {
	var l ReplicaSetList
	args := append([]string{"-n", ns, "get", "replicasets", "-o", "json"}, selectorArgs(selector)...)
	if err := k.getJSON(ctx, &l, args...); err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (k Kubectl) ListPods(ctx context.Context, ns string, selector map[string]string) ([]Pod, error) {
	var l PodList
	args := append([]string{"-n", ns, "get", "pods", "-o", "json"}, selectorArgs(selector)...)
	if err := k.getJSON(ctx, &l, args...); err != nil {
		return nil, err
	}
	return l.Items, nil
}

func (k Kubectl) Annotate(ctx context.Context, ns, deployment, key, value string) error {
	_, err := k.run(ctx, "-n", ns, "annotate", "deployment", deployment, key+"="+value, "--overwrite")
	return err
}

func (k Kubectl) getJSON(ctx context.Context, v any, args ...string) error {
	out, err := k.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(out, v); err != nil {
		return fmt.Errorf("%s %s: decode output: %w", k.bin(), strings.Join(args, " "), err)
	}
	return nil
}

func (k Kubectl) run(ctx context.Context, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, k.bin(), args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay
	if err := cmd.Run(); err != nil {
		call := k.bin() + " " + strings.Join(args, " ")
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %w: %s", call, err, msg)
		}
		return nil, fmt.Errorf("%s: %w", call, err)
	}
	return stdout.Bytes(), nil
}

func (k Kubectl) bin() string {
	if k.Bin == "" {
		return "kubectl"
	}
	return k.Bin
}

func selectorArgs(selector map[string]string) []string {
	if len(selector) == 0 {
		return nil
	}
	pairs := make([]string, 0, len(selector))
	for k, v := range selector {
		pairs = append(pairs, k+"="+v)
	}
	sort.Strings(pairs)
	return []string{"-l", strings.Join(pairs, ",")}
}

// FilterByRelease returns deployments whose Helm release annotation equals release exactly.
func FilterByRelease(deps []Deployment, release string) []Deployment {
	var out []Deployment
	for _, d := range deps {
		if d.Metadata.Annotations[AnnotationReleaseName] == release {
			out = append(out, d)
		}
	}
	return out
}
