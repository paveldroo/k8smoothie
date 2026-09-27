package kube

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func withRelease(name, release string) Deployment {
	d := Deployment{Metadata: ObjectMeta{Name: name}}
	if release != "" {
		d.Metadata.Annotations = map[string]string{AnnotationReleaseName: release}
	}
	return d
}

func withReleaseNS(name, release, ns string) Deployment {
	d := withRelease(name, release)
	d.Metadata.Annotations[AnnotationReleaseNamespace] = ns
	return d
}

func names(deps []Deployment) []string {
	var out []string
	for _, d := range deps {
		out = append(out, d.Metadata.Name)
	}
	return out
}

func TestFilterByRelease(t *testing.T) {
	deps := []Deployment{
		withRelease("speech-gp-stt-endpointer", "endpointer"),
		withRelease("speech-gp-stt-endpointer-heavy", "endpointer-heavy"),
		withRelease("speech-gp-stt-decoder", "speech-gp-stt"),
		withRelease("speech-gp-stt-encoder-decoder", "speech-gp-stt"),
		withRelease("unmanaged", ""),
		withRelease("other", "speech-gp-stt-other"),
		withReleaseNS("foreign-ns", "speech-gp-stt", "other-ns"),
		withReleaseNS("same-ns", "speech-gp-stt", "ns"),
	}

	tests := []struct {
		release string
		want    []string
	}{
		{"endpointer", []string{"speech-gp-stt-endpointer"}},
		{"endpointer-heavy", []string{"speech-gp-stt-endpointer-heavy"}},
		{"speech-gp-stt", []string{"speech-gp-stt-decoder", "speech-gp-stt-encoder-decoder", "same-ns"}},
		{"missing", nil},
	}
	for _, tt := range tests {
		t.Run(tt.release, func(t *testing.T) {
			got := names(FilterByRelease(deps, tt.release, "ns"))
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSelectorArgs(t *testing.T) {
	if got := selectorArgs(nil); got != nil {
		t.Fatalf("empty selector: got %v", got)
	}
	got := selectorArgs(map[string]string{"b": "2", "a": "1"})
	want := []string{"-l", "a=1,b=2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestOwnedBy(t *testing.T) {
	m := ObjectMeta{OwnerReferences: []OwnerReference{{UID: "a"}, {UID: "b"}}}
	if !m.OwnedBy("b") || m.OwnedBy("c") || m.OwnedBy("") {
		t.Fatal("unexpected OwnedBy result")
	}
}

func fakeKubectl(t *testing.T, script string) (Kubectl, string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho \"$@\" > \""+dir+"/args\"\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return Kubectl{Bin: bin}, filepath.Join(dir, "args")
}

func TestKubectlErrorKeepsStderr(t *testing.T) {
	k, _ := fakeKubectl(t, "echo 'Error from server (NotFound): deployments.apps \"x\" not found' >&2\nexit 1\n")
	_, err := k.GetDeployment(context.Background(), "ns", "x")
	if err == nil || !strings.Contains(err.Error(), "NotFound") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestKubectlErrorIsOneLine(t *testing.T) {
	k, _ := fakeKubectl(t, "printf 'line one\\nline two\\n' >&2\nexit 1\n")
	_, err := k.ListDeployments(context.Background(), "ns")
	if err == nil || strings.Contains(err.Error(), "\n") || !strings.Contains(err.Error(), "line one line two") {
		t.Fatalf("got %q", err)
	}
}

func TestKubectlListPods(t *testing.T) {
	k, argsFile := fakeKubectl(t, `echo '{"items":[{"metadata":{"name":"p","ownerReferences":[{"uid":"rs"}]},"status":{"phase":"Running"}}]}'`+"\n")
	pods, err := k.ListPods(context.Background(), "ns", map[string]string{"app": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || !pods[0].Metadata.OwnedBy("rs") || pods[0].Status.Phase != PodRunning {
		t.Fatalf("unexpected pods: %+v", pods)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(args)), "--request-timeout=30s -n ns get pods -o json -l app=x"; got != want {
		t.Fatalf("args %q, want %q", got, want)
	}
}

func TestGetDeploymentRejectsUnexpectedObject(t *testing.T) {
	k, _ := fakeKubectl(t, `echo '{"kind":"List","items":[]}'`+"\n")
	if _, err := k.GetDeployment(context.Background(), "ns", "x"); err == nil {
		t.Fatal("expected error for List response")
	}
	k, _ = fakeKubectl(t, `echo '{"kind":"Deployment","metadata":{"name":"x"}}'`+"\n")
	if _, err := k.GetDeployment(context.Background(), "ns", "x"); err != nil {
		t.Fatal(err)
	}
}

func TestAnnotateArgs(t *testing.T) {
	k, argsFile := fakeKubectl(t, "")
	if err := k.Annotate(context.Background(), "ns", "d", "last-activated", "t"); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(string(args)), "--request-timeout=30s -n ns annotate deployment d last-activated=t --overwrite"; got != want {
		t.Fatalf("args %q, want %q", got, want)
	}
}

func TestValidNames(t *testing.T) {
	for _, s := range []string{"a", "speech-gp-stt-endpointer", "a.b-c", strings.Repeat("a", 253)} {
		if !ValidName(s) {
			t.Errorf("ValidName(%q) = false", s)
		}
	}
	for _, s := range []string{"", "-lapp=x", "--all", "A", "a_b", "a-", strings.Repeat("a", 254)} {
		if ValidName(s) {
			t.Errorf("ValidName(%q) = true", s)
		}
	}
	if !ValidNamespace("my-ns") || ValidNamespace("my.ns") || ValidNamespace("-n") || ValidNamespace(strings.Repeat("a", 64)) {
		t.Error("unexpected ValidNamespace result")
	}
}

func TestKubectlBadJSON(t *testing.T) {
	k, _ := fakeKubectl(t, "echo not-json\n")
	if _, err := k.ListDeployments(context.Background(), "ns"); err == nil {
		t.Fatal("expected decode error")
	}
}
