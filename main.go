package main

func main() {
	// we should run `kubectl annotate deployment gracefulapp last-activated=$(date -u +"%Y-%m-%dT%H:%M:%SZ") --overwrite`
	// on deployment if there is no terminating pods and not enough pods for desire
}
