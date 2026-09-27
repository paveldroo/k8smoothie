package rollout

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/paveldroo/k8smoothie/internal/kube"
)

type counts struct {
	spec, replicas, updated, available int32
}

func mkDeploy(name string, gen, observed int64, c counts) kube.Deployment {
	spec := c.spec
	return kube.Deployment{
		Metadata: kube.ObjectMeta{
			Name:        name,
			UID:         name + "-uid",
			Generation:  gen,
			Annotations: map[string]string{kube.AnnotationRevision: "2"},
		},
		Spec: kube.DeploymentSpec{
			Replicas: &spec,
			Selector: kube.LabelSelector{MatchLabels: map[string]string{"app": name}},
		},
		Status: kube.DeploymentStatus{
			ObservedGeneration: observed,
			Replicas:           c.replicas,
			UpdatedReplicas:    c.updated,
			AvailableReplicas:  c.available,
		},
	}
}

func mkRS(d kube.Deployment, rev string) kube.ReplicaSet {
	return kube.ReplicaSet{Metadata: kube.ObjectMeta{
		Name:            d.Metadata.Name + "-rs" + rev,
		UID:             d.Metadata.Name + "-rs" + rev + "-uid",
		Annotations:     map[string]string{kube.AnnotationRevision: rev},
		OwnerReferences: []kube.OwnerReference{{Kind: "Deployment", UID: d.Metadata.UID}},
	}}
}

func withReplicas(rs kube.ReplicaSet, n int32) kube.ReplicaSet {
	rs.Spec.Replicas = &n
	return rs
}

var podSeq atomic.Int64

func mkPod(rs kube.ReplicaSet, phase string) kube.Pod {
	n := podSeq.Add(1)
	return kube.Pod{
		Metadata: kube.ObjectMeta{
			Name:            fmt.Sprintf("%s-pod%d", rs.Metadata.Name, n),
			UID:             fmt.Sprintf("pod-%d", n),
			OwnerReferences: []kube.OwnerReference{{Kind: "ReplicaSet", UID: rs.Metadata.UID}},
		},
		Status: kube.PodStatus{Phase: phase},
	}
}

func pods(rs kube.ReplicaSet, phase string, n int) []kube.Pod {
	var out []kube.Pod
	for range n {
		out = append(out, mkPod(rs, phase))
	}
	return out
}

func terminating(p kube.Pod) kube.Pod {
	return deleted(p, time.Now().Add(60*time.Second))
}

func staleTerminating(p kube.Pod) kube.Pod {
	return deleted(p, time.Now().Add(-10*time.Minute))
}

func deleted(p kube.Pod, at time.Time) kube.Pod {
	grace := int64(60)
	p.Metadata.DeletionTimestamp = &at
	p.Metadata.DeletionGracePeriodSeconds = &grace
	return p
}

func waiting(p kube.Pod, reason string) kube.Pod {
	p.Status.ContainerStatuses = append(p.Status.ContainerStatuses, kube.ContainerStatus{
		Name:  "app",
		State: kube.ContainerState{Waiting: &kube.ContainerStateWaiting{Reason: reason}},
	})
	return p
}

func initWaiting(p kube.Pod, reason string) kube.Pod {
	p.Status.InitContainerStatuses = append(p.Status.InitContainerStatuses, kube.ContainerStatus{
		Name:  "init",
		State: kube.ContainerState{Waiting: &kube.ContainerStateWaiting{Reason: reason}},
	})
	return p
}

func cat(groups ...[]kube.Pod) []kube.Pod {
	var out []kube.Pod
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}
