package errors

import "errors"

var (
	ErrNoDeploymentFound  = errors.New("🙈 no deployment was found, it's impossible to manage deploy")
	ErrNoReplicaSetsFound = errors.New("🙈 no replicasets with active conditions were found, it's impossible to manage deploy")
)
