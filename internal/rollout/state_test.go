package rollout

import (
	"strings"
	"testing"
	"time"

	"github.com/paveldroo/k8smoothie/internal/kube"
)

func TestEvaluate(t *testing.T) {
	d := mkDeploy("endpointer", 2, 2, counts{3, 3, 3, 3})
	oldRS := mkRS(d, "1")
	curRS := mkRS(d, "2")
	rsList := []kube.ReplicaSet{oldRS, curRS}

	heavy := mkDeploy("endpointer-heavy", 1, 1, counts{1, 1, 1, 1})
	heavyRS := mkRS(heavy, "2")

	with := func(c counts) kube.Deployment {
		return mkDeploy("endpointer", 2, 2, c)
	}
	pdeFailed := with(counts{3, 3, 1, 1})
	pdeFailed.Status.Conditions = []kube.DeploymentCondition{{Type: "Progressing", Status: "False", Reason: "ProgressDeadlineExceeded", Message: "too slow"}}

	evicted := mkPod(curRS, kube.PodFailed)
	evicted.Status.Reason = "Evicted"

	tests := []struct {
		name    string
		d       kube.Deployment
		rs      []kube.ReplicaSet
		pods    []kube.Pod
		want    State
		warning bool
	}{
		{
			name: "generation not observed",
			d:    mkDeploy("endpointer", 3, 2, counts{3, 3, 3, 3}),
			rs:   rsList,
			pods: pods(curRS, kube.PodRunning, 3),
			want: StateWaiting,
		},
		{
			name: "updated less than spec",
			d:    with(counts{3, 3, 2, 3}),
			rs:   rsList,
			pods: cat(pods(curRS, kube.PodRunning, 2), pods(oldRS, kube.PodRunning, 1)),
			want: StateKick,
		},
		{
			name: "old replicas remain",
			d:    with(counts{3, 4, 3, 3}),
			rs:   rsList,
			pods: cat(pods(curRS, kube.PodRunning, 3), []kube.Pod{terminating(mkPod(oldRS, kube.PodRunning))}),
			want: StateWaiting,
		},
		{
			name: "available less than updated",
			d:    with(counts{3, 3, 3, 2}),
			rs:   rsList,
			pods: pods(curRS, kube.PodRunning, 3),
			want: StateKick,
		},
		{
			name: "done",
			d:    d,
			rs:   rsList,
			pods: pods(curRS, kube.PodRunning, 3),
			want: StateDone,
		},
		{
			name: "zero replicas",
			d:    with(counts{0, 0, 0, 0}),
			rs:   rsList,
			want: StateDone,
		},
		{
			name: "maxUnavailable=0 mid-rollout is not done",
			d:    with(counts{3, 4, 1, 3}),
			rs:   rsList,
			pods: cat(pods(oldRS, kube.PodRunning, 3), pods(curRS, kube.PodPending, 1)),
			want: StateWaiting,
		},
		{
			name:    "ProgressDeadlineExceeded keeps waiting",
			d:       pdeFailed,
			rs:      rsList,
			pods:    cat(pods(curRS, kube.PodPending, 2), pods(curRS, kube.PodRunning, 1)),
			want:    StateWaiting,
			warning: true,
		},
		{
			name: "CrashLoopBackOff in current replicaset fails",
			d:    with(counts{3, 3, 3, 2}),
			rs:   rsList,
			pods: cat(pods(curRS, kube.PodRunning, 2), []kube.Pod{waiting(mkPod(curRS, kube.PodRunning), "CrashLoopBackOff")}),
			want: StateFailed,
		},
		{
			name: "ImagePullBackOff in init container fails",
			d:    with(counts{3, 3, 3, 2}),
			rs:   rsList,
			pods: []kube.Pod{initWaiting(mkPod(curRS, kube.PodPending), "ImagePullBackOff")},
			want: StateFailed,
		},
		{
			name: "ErrImagePull is transient",
			d:    with(counts{3, 3, 3, 2}),
			rs:   rsList,
			pods: []kube.Pod{waiting(mkPod(curRS, kube.PodPending), "ErrImagePull")},
			want: StateWaiting,
		},
		{
			name: "CrashLoopBackOff in old replicaset ignored",
			d:    with(counts{3, 3, 2, 2}),
			rs:   rsList,
			pods: cat(pods(curRS, kube.PodRunning, 2), []kube.Pod{waiting(mkPod(oldRS, kube.PodRunning), "CrashLoopBackOff")}),
			want: StateKick,
		},
		{
			name: "CrashLoopBackOff in prefix-sharing deployment ignored",
			d:    with(counts{3, 3, 2, 2}),
			rs:   []kube.ReplicaSet{oldRS, curRS, heavyRS},
			pods: cat(pods(curRS, kube.PodRunning, 2), []kube.Pod{waiting(mkPod(heavyRS, kube.PodRunning), "CrashLoopBackOff")}),
			want: StateKick,
		},
		{
			name: "evicted pod ignored",
			d:    with(counts{3, 3, 2, 2}),
			rs:   rsList,
			pods: cat(pods(curRS, kube.PodRunning, 2), []kube.Pod{evicted}),
			want: StateKick,
		},
		{
			name: "zero pods kicks",
			d:    with(counts{3, 0, 0, 0}),
			rs:   rsList,
			want: StateKick,
		},
		{
			name: "current replicaset not created yet kicks",
			d:    with(counts{3, 0, 0, 0}),
			rs:   []kube.ReplicaSet{oldRS},
			want: StateKick,
		},
		{
			name: "terminating pods block kick",
			d:    with(counts{3, 3, 0, 0}),
			rs:   rsList,
			pods: []kube.Pod{terminating(mkPod(oldRS, kube.PodRunning)), terminating(mkPod(curRS, kube.PodRunning))},
			want: StateWaiting,
		},
		{
			name: "terminating pod of other deployment does not block kick",
			d:    with(counts{3, 0, 0, 0}),
			rs:   []kube.ReplicaSet{oldRS, curRS, heavyRS},
			pods: []kube.Pod{terminating(mkPod(heavyRS, kube.PodRunning))},
			want: StateKick,
		},
		{
			name: "pending in current replicaset blocks kick",
			d:    with(counts{3, 3, 3, 2}),
			rs:   rsList,
			pods: cat(pods(curRS, kube.PodRunning, 2), pods(curRS, kube.PodPending, 1)),
			want: StateWaiting,
		},
		{
			name: "pending in old replicaset does not block kick",
			d:    with(counts{3, 3, 2, 2}),
			rs:   rsList,
			pods: cat(pods(curRS, kube.PodRunning, 2), pods(oldRS, kube.PodPending, 1)),
			want: StateKick,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Evaluate(tt.d, tt.rs, tt.pods, time.Now())
			if v.State != tt.want {
				t.Fatalf("state %s, want %s (reason: %s)", v.State, tt.want, v.Reason)
			}
			if v.Reason == "" {
				t.Fatal("empty reason")
			}
			if (v.Warning != "") != tt.warning {
				t.Fatalf("warning %q, want present=%v", v.Warning, tt.warning)
			}
		})
	}
}

func TestEvaluateStuckTerminatingPastGraceKicks(t *testing.T) {
	d := mkDeploy("app", 2, 2, counts{3, 3, 0, 0})
	old := mkRS(d, "1")
	v := Evaluate(d, []kube.ReplicaSet{old, mkRS(d, "2")}, []kube.Pod{staleTerminating(mkPod(old, kube.PodRunning))}, time.Now())
	if v.State != StateKick || !strings.Contains(v.Reason, "past grace period") {
		t.Fatalf("state %s reason %q", v.State, v.Reason)
	}
}

func TestEvaluatePausedFails(t *testing.T) {
	d := mkDeploy("app", 2, 2, counts{3, 3, 1, 1})
	d.Spec.Paused = true
	if v := Evaluate(d, nil, nil, time.Now()); v.State != StateFailed {
		t.Fatalf("state %s, want failed", v.State)
	}
	done := mkDeploy("app", 2, 2, counts{3, 3, 3, 3})
	done.Spec.Paused = true
	if v := Evaluate(done, nil, nil, time.Now()); v.State != StateDone {
		t.Fatalf("paused but rolled out: state %s, want done", v.State)
	}
}

func TestEvaluateReplicaFailureInReason(t *testing.T) {
	d := mkDeploy("app", 2, 2, counts{3, 0, 0, 0})
	d.Status.Conditions = []kube.DeploymentCondition{{Type: "ReplicaFailure", Status: "True", Reason: "FailedCreate", Message: "exceeded quota: max-pods"}}
	v := Evaluate(d, []kube.ReplicaSet{mkRS(d, "2")}, nil, time.Now())
	if v.State != StateKick || !strings.Contains(v.Reason, "exceeded quota: max-pods") {
		t.Fatalf("state %s reason %q", v.State, v.Reason)
	}
}

func TestEvaluateNilReplicasDefaultsToOne(t *testing.T) {
	d := mkDeploy("x", 1, 1, counts{0, 1, 1, 1})
	d.Spec.Replicas = nil
	if v := Evaluate(d, nil, nil, time.Now()); v.State != StateDone {
		t.Fatalf("state %s, want done", v.State)
	}
}
