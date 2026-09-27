package rollout

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/paveldroo/k8smoothie/internal/kube"
)

type snapshot struct {
	d    kube.Deployment
	rs   []kube.ReplicaSet
	pods []kube.Pod
	err  error
}

type fakeClient struct {
	mu    sync.Mutex
	steps map[string][]snapshot
	pos   map[string]int
	kicks map[string]int
}

func newFake(steps map[string][]snapshot) *fakeClient {
	return &fakeClient{steps: steps, pos: map[string]int{}, kicks: map[string]int{}}
}

func (f *fakeClient) current(name string) snapshot {
	s := f.steps[name]
	i := min(f.pos[name], len(s)-1)
	return s[i]
}

func (f *fakeClient) listed(name string) snapshot {
	s := f.steps[name]
	return s[min(max(f.pos[name]-1, 0), len(s)-1)]
}

func (f *fakeClient) GetDeployment(_ context.Context, _, name string) (kube.Deployment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := f.current(name)
	f.pos[name]++
	return s.d, s.err
}

func (f *fakeClient) ListDeployments(context.Context, string) ([]kube.Deployment, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClient) ListReplicaSets(_ context.Context, _ string, sel map[string]string) ([]kube.ReplicaSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listed(sel["app"]).rs, nil
}

func (f *fakeClient) ListPods(_ context.Context, _ string, sel map[string]string) ([]kube.Pod, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.listed(sel["app"]).pods, nil
}

func (f *fakeClient) Annotate(_ context.Context, _, name, key, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if key != kickAnnotation || value == "" {
		return errors.New("bad annotation")
	}
	f.kicks[name]++
	return nil
}

func (f *fakeClient) kickCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.kicks[name]
}

func stuck(name string) snapshot {
	d := mkDeploy(name, 2, 2, counts{3, 0, 0, 0})
	return snapshot{d: d, rs: []kube.ReplicaSet{mkRS(d, "1"), withReplicas(mkRS(d, "2"), 3)}}
}

func pendingSnap(name string) snapshot {
	d := mkDeploy(name, 2, 2, counts{3, 1, 1, 0})
	rs := mkRS(d, "2")
	return snapshot{d: d, rs: []kube.ReplicaSet{rs}, pods: pods(rs, kube.PodPending, 1)}
}

func doneSnap(name string) snapshot {
	d := mkDeploy(name, 2, 2, counts{3, 3, 3, 3})
	rs := mkRS(d, "2")
	return snapshot{d: d, rs: []kube.ReplicaSet{rs}, pods: pods(rs, kube.PodRunning, 3)}
}

func crashSnap(name string) snapshot {
	d := mkDeploy(name, 2, 2, counts{3, 3, 3, 2})
	rs := mkRS(d, "2")
	return snapshot{d: d, rs: []kube.ReplicaSet{rs}, pods: []kube.Pod{waiting(mkPod(rs, kube.PodRunning), "CrashLoopBackOff")}}
}

func watcher(c kube.Client, out *bytes.Buffer) Watcher {
	return Watcher{Client: c, Namespace: "ns", Frequency: time.Millisecond, Out: out}
}

func TestWatchKicksWhileStuck(t *testing.T) {
	f := newFake(map[string][]snapshot{"app": {stuck("app"), stuck("app"), stuck("app"), doneSnap("app")}})
	var out bytes.Buffer
	r := watcher(f, &out).Watch(context.Background(), "app")
	if r.Status != Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Reason)
	}
	if got := f.kickCount("app"); got != 3 {
		t.Fatalf("kicks %d, want 3", got)
	}
	if !strings.Contains(out.String(), "[app] ") {
		t.Fatalf("missing log prefix: %s", out.String())
	}
}

func TestWatchNoKickWhilePending(t *testing.T) {
	f := newFake(map[string][]snapshot{"app": {pendingSnap("app"), pendingSnap("app"), doneSnap("app")}})
	r := watcher(f, &bytes.Buffer{}).Watch(context.Background(), "app")
	if r.Status != Succeeded || f.kickCount("app") != 0 {
		t.Fatalf("status %s, kicks %d", r.Status, f.kickCount("app"))
	}
}

func TestWatchTimeout(t *testing.T) {
	f := newFake(map[string][]snapshot{"app": {pendingSnap("app")}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	r := watcher(f, &bytes.Buffer{}).Watch(ctx, "app")
	if r.Status != TimedOut || r.Status.Emoji() != "⏰" {
		t.Fatalf("status %s, want timed out", r.Status)
	}
}

func TestWatchCancel(t *testing.T) {
	f := newFake(map[string][]snapshot{"app": {pendingSnap("app")}})
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	r := watcher(f, &bytes.Buffer{}).Watch(ctx, "app")
	if r.Status != Canceled {
		t.Fatalf("status %s, want canceled", r.Status)
	}
}

func TestWatchKubectlError(t *testing.T) {
	f := newFake(map[string][]snapshot{"app": {{err: errors.New("forbidden: boom")}}})
	r := watcher(f, &bytes.Buffer{}).Watch(context.Background(), "app")
	if r.Status != Failed || !strings.Contains(r.Reason, "forbidden: boom") {
		t.Fatalf("status %s reason %q", r.Status, r.Reason)
	}
}

func TestWatchToleratesTransientErrors(t *testing.T) {
	blip := snapshot{err: errors.New("etcdserver: leader changed")}
	f := newFake(map[string][]snapshot{"app": {blip, blip, doneSnap("app")}})
	var out bytes.Buffer
	r := watcher(f, &out).Watch(context.Background(), "app")
	if r.Status != Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Reason)
	}
	if !strings.Contains(out.String(), "2/3 consecutive errors") {
		t.Fatalf("missing error log:\n%s", out.String())
	}
}

func TestWatchErrorStreakResets(t *testing.T) {
	blip := snapshot{err: errors.New("blip")}
	f := newFake(map[string][]snapshot{"app": {blip, blip, stuck("app"), blip, blip, doneSnap("app")}})
	if r := watcher(f, &bytes.Buffer{}).Watch(context.Background(), "app"); r.Status != Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Reason)
	}
}

func TestWatchFailureNeedsConfirmation(t *testing.T) {
	f := newFake(map[string][]snapshot{"app": {crashSnap("app"), pendingSnap("app"), crashSnap("app"), doneSnap("app")}})
	var out bytes.Buffer
	r := watcher(f, &out).Watch(context.Background(), "app")
	if r.Status != Succeeded {
		t.Fatalf("status %s: %s", r.Status, r.Reason)
	}
	if !strings.Contains(out.String(), "confirming on next check") {
		t.Fatalf("missing confirmation log:\n%s", out.String())
	}
}

func TestWaitAllIndependent(t *testing.T) {
	slow := []snapshot{stuck("slow"), stuck("slow"), stuck("slow"), stuck("slow"), doneSnap("slow")}
	f := newFake(map[string][]snapshot{
		"fast":  {doneSnap("fast")},
		"slow":  slow,
		"crash": {stuck("crash"), crashSnap("crash")},
	})
	var out bytes.Buffer
	results := watcher(f, &out).WaitAll(context.Background(), []string{"fast", "slow", "crash"})

	want := []struct {
		name   string
		status Status
	}{{"fast", Succeeded}, {"slow", Succeeded}, {"crash", Failed}}
	if len(results) != len(want) {
		t.Fatalf("got %d results", len(results))
	}
	for i, w := range want {
		if results[i].Name != w.name || results[i].Status != w.status {
			t.Fatalf("result %d: %+v, want %s %s", i, results[i], w.name, w.status)
		}
	}
	if f.kickCount("fast") != 0 || f.kickCount("slow") != 4 {
		t.Fatalf("kicks fast=%d slow=%d", f.kickCount("fast"), f.kickCount("slow"))
	}
}

func TestWaitAllTimeoutMixed(t *testing.T) {
	f := newFake(map[string][]snapshot{
		"done":  {doneSnap("done")},
		"stuck": {pendingSnap("stuck")},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	results := watcher(f, &bytes.Buffer{}).WaitAll(ctx, []string{"done", "stuck"})
	if results[0].Status != Succeeded || results[1].Status != TimedOut {
		t.Fatalf("results %+v", results)
	}
}
