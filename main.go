package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/paveldroo/k8smoothie/internal/kube"
	"github.com/paveldroo/k8smoothie/internal/rollout"
)

var (
	version       = "dev"
	frequencyUnit = time.Second
)

const (
	usageExitCode = 2
	maxFrequency  = 3600
	kickInterval  = 15 * time.Second
)

type config struct {
	namespace     string
	deployments   []string
	release       string
	timeout       time.Duration
	frequency     time.Duration
	errorExitCode int
	showVersion   bool
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	context.AfterFunc(ctx, stop)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr, kube.Kubectl{})
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, client kube.Client) int {
	cfg, err := parseFlags(args, stderr)
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if err != nil {
		fmt.Fprintf(stderr, "🙈 %s\n", err)
		return usageExitCode
	}
	if cfg.showVersion {
		fmt.Fprintf(stdout, "k8smoothie %s\n", version)
		return 0
	}

	if cfg.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.timeout)
		defer cancel()
	}

	targets := cfg.deployments
	if cfg.release != "" {
		targets, err = discover(ctx, client, cfg, stdout)
		if err != nil {
			fmt.Fprintf(stdout, "💥 %s\n", err)
			return cfg.errorExitCode
		}
	}

	fmt.Fprintf(stdout, "🧋 k8smoothie: namespace=%s, deployments=%s, timeout=%s, frequency=%s, error-exit-code=%d\n",
		cfg.namespace, strings.Join(targets, ","), timeoutString(cfg.timeout), cfg.frequency, cfg.errorExitCode)

	w := rollout.Watcher{Client: client, Namespace: cfg.namespace, Frequency: cfg.frequency, KickInterval: kickInterval, Out: stdout}
	results := w.WaitAll(ctx, targets)

	fmt.Fprintln(stdout, "🧋 Summary:")
	ok := true
	for _, r := range results {
		fmt.Fprintf(stdout, "  %s %s: %s, %s (%s)\n", r.Status.Emoji(), r.Name, r.Status, r.Reason, r.Duration.Round(time.Second))
		if r.Status != rollout.Succeeded {
			ok = false
		}
	}
	if !ok {
		return cfg.errorExitCode
	}
	fmt.Fprintln(stdout, "🎉🎉🎉 all deployments rolled out")
	return 0
}

func parseFlags(args []string, stderr io.Writer) (config, error) {
	fs := flag.NewFlagSet("k8smoothie", flag.ContinueOnError)
	fs.SetOutput(stderr)
	namespace := fs.String("namespace", "", "namespace of the deployments (required)")
	deployments := fs.String("deployment", "", "comma-separated deployment names")
	release := fs.String("helm-release", "", "wait for all deployments of this Helm release (by meta.helm.sh/release-name annotation)")
	timeout := fs.Duration("timeout", 0, "overall timeout for all deployments, e.g. 30m (0 = none)")
	frequency := fs.Int("frequency", 5, "polling frequency in seconds")
	errorExitCode := fs.Int("error-exit-code", 1, "exit code on rollout failure, timeout or runtime error")
	showVersion := fs.Bool("version", false, "print version and exit")

	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	cfg := config{
		namespace:     *namespace,
		release:       *release,
		timeout:       *timeout,
		frequency:     time.Duration(*frequency) * frequencyUnit,
		errorExitCode: *errorExitCode,
		showVersion:   *showVersion,
	}
	if cfg.showVersion {
		return cfg, nil
	}

	switch {
	case fs.NArg() > 0:
		return cfg, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	case cfg.namespace == "":
		return cfg, errors.New("-namespace is required")
	case !kube.ValidNamespace(cfg.namespace):
		return cfg, fmt.Errorf("-namespace %q is not a valid namespace name", cfg.namespace)
	case *deployments == "" && cfg.release == "":
		return cfg, errors.New("one of -deployment or -helm-release is required")
	case *deployments != "" && cfg.release != "":
		return cfg, errors.New("-deployment and -helm-release are mutually exclusive")
	case *frequency <= 0 || *frequency > maxFrequency:
		return cfg, fmt.Errorf("-frequency must be between 1 and %d seconds", maxFrequency)
	case *errorExitCode < 0 || *errorExitCode > 255:
		return cfg, errors.New("-error-exit-code must be between 0 and 255")
	case cfg.timeout < 0:
		return cfg, errors.New("-timeout must not be negative")
	}

	if *deployments != "" {
		names, err := splitNames(*deployments)
		if err != nil {
			return cfg, err
		}
		cfg.deployments = names
	}
	return cfg, nil
}

func splitNames(s string) ([]string, error) {
	seen := map[string]bool{}
	var names []string
	for n := range strings.SplitSeq(s, ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			return nil, fmt.Errorf("-deployment has an empty name: %q", s)
		}
		if !kube.ValidName(n) {
			return nil, fmt.Errorf("-deployment %q is not a valid deployment name", n)
		}
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	return names, nil
}

func discover(ctx context.Context, client kube.Client, cfg config, out io.Writer) ([]string, error) {
	ns, release := cfg.namespace, cfg.release
	var deps []kube.Deployment
	for attempt := 1; ; attempt++ {
		var err error
		deps, err = client.ListDeployments(ctx, ns)
		if err == nil {
			break
		}
		if ctx.Err() != nil || attempt >= rollout.MaxConsecutiveErrors {
			return nil, fmt.Errorf("discover deployments: %w", err)
		}
		fmt.Fprintf(out, "🙈 discover deployments: %s (%d/%d consecutive errors)\n", err, attempt, rollout.MaxConsecutiveErrors)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("discover deployments: %w", ctx.Err())
		case <-time.After(cfg.frequency):
		}
	}
	var names []string
	for _, d := range kube.FilterByRelease(deps, release, ns) {
		if d.Metadata.DeletionTimestamp != nil {
			continue
		}
		names = append(names, d.Metadata.Name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("%w for helm release %q in namespace %q", rollout.ErrNoDeployments, release, ns)
	}
	sort.Strings(names)
	return names, nil
}

func timeoutString(d time.Duration) string {
	if d == 0 {
		return "none"
	}
	return d.String()
}
