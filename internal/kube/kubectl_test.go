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
	}

	tests := []struct {
		release string
		want    []string
	}{
		{"endpointer", []string{"speech-gp-stt-endpointer"}},
		{"endpointer-heavy", []string{"speech-gp-stt-endpointer-heavy"}},
		{"speech-gp-stt", []string{"speech-gp-stt-decoder", "speech-gp-stt-encoder-decoder"}},
		{"missing", nil},
	}
	for _, tt := range tests {
		t.Run(tt.release, func(t *testing.T) {
			got := names(FilterByRelease(deps, tt.release))
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
	if got, want := strings.TrimSpace(string(args)), "-n ns get pods -o json -l app=x"; got != want {
		t.Fatalf("args %q, want %q", got, want)
	}
}

func TestKubectlBadJSON(t *testing.T) {
	k, _ := fakeKubectl(t, "echo not-json\n")
	if _, err := k.ListDeployments(context.Background(), "ns"); err == nil {
		t.Fatal("expected decode error")
	}
}
