package kube

import "time"

const (
	AnnotationRevision    = "deployment.kubernetes.io/revision"
	AnnotationReleaseName = "meta.helm.sh/release-name"
)

const (
	PodPending   = "Pending"
	PodRunning   = "Running"
	PodSucceeded = "Succeeded"
	PodFailed    = "Failed"
)

type OwnerReference struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	UID  string `json:"uid"`
}

type ObjectMeta struct {
	Name                       string            `json:"name"`
	UID                        string            `json:"uid"`
	Generation                 int64             `json:"generation"`
	Annotations                map[string]string `json:"annotations"`
	OwnerReferences            []OwnerReference  `json:"ownerReferences"`
	DeletionTimestamp          *time.Time        `json:"deletionTimestamp"`
	DeletionGracePeriodSeconds *int64            `json:"deletionGracePeriodSeconds"`
}

// OwnedBy reports whether the object has an owner reference with the given uid.
func (m ObjectMeta) OwnedBy(uid string) bool {
	if uid == "" {
		return false
	}
	for _, o := range m.OwnerReferences {
		if o.UID == uid {
			return true
		}
	}
	return false
}

type LabelSelector struct {
	MatchLabels map[string]string `json:"matchLabels"`
}

type DeploymentSpec struct {
	Replicas *int32        `json:"replicas"`
	Paused   bool          `json:"paused"`
	Selector LabelSelector `json:"selector"`
}

type DeploymentCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type DeploymentStatus struct {
	ObservedGeneration int64                 `json:"observedGeneration"`
	Replicas           int32                 `json:"replicas"`
	UpdatedReplicas    int32                 `json:"updatedReplicas"`
	AvailableReplicas  int32                 `json:"availableReplicas"`
	Conditions         []DeploymentCondition `json:"conditions"`
}

type Deployment struct {
	Kind     string           `json:"kind"`
	Metadata ObjectMeta       `json:"metadata"`
	Spec     DeploymentSpec   `json:"spec"`
	Status   DeploymentStatus `json:"status"`
}

// DesiredReplicas returns spec.replicas, defaulting to 1 like the API server.
func (d Deployment) DesiredReplicas() int32 {
	if d.Spec.Replicas == nil {
		return 1
	}
	return *d.Spec.Replicas
}

type DeploymentList struct {
	Items []Deployment `json:"items"`
}

type ReplicaSet struct {
	Metadata ObjectMeta `json:"metadata"`
}

type ReplicaSetList struct {
	Items []ReplicaSet `json:"items"`
}

type ContainerStateWaiting struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

type ContainerState struct {
	Waiting *ContainerStateWaiting `json:"waiting"`
}

type ContainerStatus struct {
	Name  string         `json:"name"`
	State ContainerState `json:"state"`
}

type PodStatus struct {
	Phase                 string            `json:"phase"`
	Reason                string            `json:"reason"`
	ContainerStatuses     []ContainerStatus `json:"containerStatuses"`
	InitContainerStatuses []ContainerStatus `json:"initContainerStatuses"`
}

type Pod struct {
	Metadata ObjectMeta `json:"metadata"`
	Status   PodStatus  `json:"status"`
}

type PodList struct {
	Items []Pod `json:"items"`
}
