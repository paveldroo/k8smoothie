package structs

import "time"

type Spec struct {
	Replicas int `json:"replicas"`
}

type Deployment struct {
	Spec *Spec `json:"spec"`
}

type Status struct {
	AvailableReplicas int `json:"availableReplicas"`
}

type ReplicaSet struct {
	Status *Status `json:"status"`
}

type ReplicaSets struct {
	Items []ReplicaSet `json:"items"`
}

type Metadata struct {
	DeletionTimestamp *time.Time `json:"deletionTimestamp"`
}

type PodStatus struct {
	Phase string `json:"phase"`
}

type Pod struct {
	Metadata Metadata  `json:"metadata"`
	Status   PodStatus `json:"status"`
}

type Pods struct {
	Items []Pod `json:"items"`
}
