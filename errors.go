package main

import "errors"

var (
	ErrNoDeploymentFound  = errors.New("🙈 no deployment was found, it's impossible to manage deploy")
	ErrNoReplicaSetsFound = errors.New("🙈 no replicasets were found, it's impossible to manage deploy")
)
