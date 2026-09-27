package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/paveldroo/k8smoothie/internal/kube"
)

func TestMain(m *testing.M) {
	frequencyUnit = time.Millisecond
	os.Exit(m.Run())
}

type fakeClient struct {
	deps     []kube.Deployment
	getErr   map[string]error
	listErrs *int
}

func (f fakeClient) GetDeployment(_ context.Context, _, name string) (kube.Deployment, error) {
	if err := f.getErr[name]; err != nil {
		return kube.Deployment{}, err
	}
	for _, d := range f.deps {
		if d.Metadata.Name == name {
			return d, nil
		}
	}
	return kube.Deployment{}, errors.New("not found")
}

func (f fakeClient) ListDeployments(context.Context, string) ([]kube.Deployment, error) {
	if f.listErrs != nil && *f.listErrs > 0 {
		*f.listErrs--
		return nil, errors.New("apiserver unavailable")
	}
	return f.deps, nil
}

func (f fakeClient) ListReplicaSets(context.Context, string, map[string]string) ([]kube.ReplicaSet, error) {
	return nil, nil
}

func (f fakeClient) ListPods(context.Context, string, map[string]string) ([]kube.Pod, error) {
	return nil, nil
}

func (f fakeClient) Annotate(context.Context, string, string, string, string) error {
	return nil
}

func doneDeploy(name, release string) kube.Deployment {
	one := int32(1)
	return kube.Deployment{
		Metadata: kube.ObjectMeta{Name: name, Generation: 1, Annotations: map[string]string{kube.AnnotationReleaseName: release}},
		Spec:     kube.DeploymentSpec{Replicas: &one},
		Status:   kube.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1},
	}
}

func stuckDeploy(name, release string) kube.Deployment {
	d := doneDeploy(name, release)
	d.Status.UpdatedReplicas = 0
	return d
}

func deletingDeploy(name, release string) kube.Deployment {
	d := stuckDeploy(name, release)
	now := time.Now()
	d.Metadata.DeletionTimestamp = &now
	return d
}

func runT(t *testing.T, client kube.Client, args ...string) (int, string) {
	t.Helper()
	var out bytes.Buffer
	code := run(context.Background(), args, &out, &out, client)
	return code, out.String()
}

func TestUsageErrorsExit2(t *testing.T) {
	tests := map[string][]string{
		"no namespace":     {"-deployment=a"},
		"no target":        {"-namespace=ns"},
		"both targets":     {"-namespace=ns", "-deployment=a", "-helm-release=r"},
		"empty name":       {"-namespace=ns", "-deployment=a,,b"},
		"zero frequency":   {"-namespace=ns", "-deployment=a", "-frequency=0"},
		"negative timeout": {"-namespace=ns", "-deployment=a", "-timeout=-1s"},
		"bad timeout":      {"-namespace=ns", "-deployment=a", "-timeout=5"},
		"unknown flag":     {"-namespace=ns", "-deployment=a", "-nope"},
		"positional":       {"-namespace=ns", "-deployment=a", "extra"},
		"flag injection":   {"-namespace=ns", "-deployment=-lapp=x"},
		"bad namespace":    {"-namespace=-lx", "-deployment=a"},
		"exit code 256":    {"-namespace=ns", "-deployment=a", "-error-exit-code=256"},
		"exit code -1":     {"-namespace=ns", "-deployment=a", "-error-exit-code=-1"},
		"huge frequency":   {"-namespace=ns", "-deployment=a", "-frequency=9999999999999"},
	}
	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			code, out := runT(t, fakeClient{}, append([]string{"-error-exit-code=0"}, args...)...)
			if code != usageExitCode {
				t.Fatalf("code %d, want %d: %s", code, usageExitCode, out)
			}
		})
	}
}

func TestVersion(t *testing.T) {
	code, out := runT(t, fakeClient{}, "--version")
	if code != 0 || !strings.Contains(out, "k8smoothie dev") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestHelmReleaseDiscovery(t *testing.T) {
	c := fakeClient{deps: []kube.Deployment{
		doneDeploy("speech-gp-stt-endpointer", "speech-gp-stt"),
		doneDeploy("speech-gp-stt-endpointer-heavy", "speech-gp-stt"),
		stuckDeploy("foreign", "other"),
		deletingDeploy("speech-gp-stt-old", "speech-gp-stt"),
	}}
	code, out := runT(t, c, "-namespace=ns", "--helm-release=speech-gp-stt", "-frequency=1")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	for _, want := range []string{"✅ speech-gp-stt-endpointer:", "✅ speech-gp-stt-endpointer-heavy:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "foreign") || strings.Contains(out, "speech-gp-stt-old") {
		t.Fatalf("foreign deployment watched:\n%s", out)
	}
}

func TestHelmReleaseNoDeployments(t *testing.T) {
	code, out := runT(t, fakeClient{deps: []kube.Deployment{doneDeploy("a", "other")}}, "-namespace=ns", "-helm-release=r", "-error-exit-code=7")
	if code != 7 || !strings.Contains(out, "no deployments found") {
		t.Fatalf("code %d: %s", code, out)
	}
}

func TestExplicitFailureUsesErrorExitCode(t *testing.T) {
	c := fakeClient{
		deps:   []kube.Deployment{doneDeploy("a", "")},
		getErr: map[string]error{"b": errors.New("forbidden")},
	}
	code, out := runT(t, c, "-namespace=ns", "-deployment=a, b", "-error-exit-code=0")
	if code != 0 {
		t.Fatalf("code %d: %s", code, out)
	}
	if !strings.Contains(out, "✅ a:") || !strings.Contains(out, "💥 b:") || !strings.Contains(out, "forbidden") {
		t.Fatalf("unexpected summary:\n%s", out)
	}
	code, _ = runT(t, c, "-namespace=ns", "-deployment=a,b")
	if code != 1 {
		t.Fatalf("code %d, want 1", code)
	}
}

func TestTimeout(t *testing.T) {
	start := time.Now()
	code, out := runT(t, fakeClient{deps: []kube.Deployment{stuckDeploy("a", "")}}, "-namespace=ns", "-deployment=a", "-timeout=50ms", "-error-exit-code=3")
	if code != 3 || !strings.Contains(out, "⏰ a:") {
		t.Fatalf("code %d: %s", code, out)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("timeout not honored")
	}
}

func TestCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	var out bytes.Buffer
	code := run(ctx, []string{"-namespace=ns", "-deployment=a", "-error-exit-code=4"}, &out, &out, fakeClient{deps: []kube.Deployment{stuckDeploy("a", "")}})
	if code != 4 || !strings.Contains(out.String(), "🛑 a:") {
		t.Fatalf("code %d: %s", code, out.String())
	}
}

func TestDiscoveryRetries(t *testing.T) {
	errs := 2
	c := fakeClient{deps: []kube.Deployment{doneDeploy("a", "r")}, listErrs: &errs}
	code, out := runT(t, c, "-namespace=ns", "-helm-release=r")
	if code != 0 || !strings.Contains(out, "2/3 consecutive errors") || !strings.Contains(out, "✅ a:") {
		t.Fatalf("code %d: %s", code, out)
	}
	errs = 3
	code, out = runT(t, c, "-namespace=ns", "-helm-release=r", "-error-exit-code=5")
	if code != 5 || !strings.Contains(out, "apiserver unavailable") {
		t.Fatalf("code %d: %s", code, out)
	}
}
