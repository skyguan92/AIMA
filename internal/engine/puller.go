package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path"
	"strings"
)

// PullOptions configures an image pull operation.
type PullOptions struct {
	Image          string
	Tag            string
	Registries     []string
	Runner         CommandRunner
	ExpectedDigest string // OCI content digest e.g. "sha256:abc123..." (optional)
}

// ImageExists reports whether a container image is already present in the local runtime.
// Tries crictl (with K3S fallback) first, then docker. Returns false on any error.
func ImageExists(ctx context.Context, image, tag string, runner CommandRunner) bool {
	ref := image + ":" + tag
	if out, err := runCrictl(ctx, runner, "images", "--quiet", ref); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return true
	}
	if out, err := runner.Run(ctx, "docker", "images", "-q", ref); err == nil && len(strings.TrimSpace(string(out))) > 0 {
		return true
	}
	return false
}

// Pull downloads a container image, trying registries in order.
// Falls back from crictl to docker if crictl fails.
func Pull(ctx context.Context, opts PullOptions) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("pull image %s:%s: %w", opts.Image, opts.Tag, err)
	}

	if len(opts.Registries) == 0 {
		return fmt.Errorf("pull image %s:%s: no registries configured", opts.Image, opts.Tag)
	}

	var lastErr error
	for _, registry := range opts.Registries {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("pull image %s:%s: %w", opts.Image, opts.Tag, err)
		}

		ref := buildImageRef(registry, opts.Image, opts.Tag)

		// Try crictl first (with K3S fallback)
		if _, err := runCrictl(ctx, opts.Runner, "pull", ref); err == nil {
			verifyDigest(ctx, opts.Runner, ref, opts.ExpectedDigest)
			return nil
		}

		// Fallback to docker
		if _, err := opts.Runner.Run(ctx, "docker", "pull", ref); err == nil {
			verifyDigest(ctx, opts.Runner, ref, opts.ExpectedDigest)
			return nil
		} else {
			lastErr = err
		}
	}

	return fmt.Errorf("pull image %s:%s: all registries failed: %w", opts.Image, opts.Tag, lastErr)
}

// ImageExistsInContainerd checks whether image exists in containerd (K3S) store only.
// Unlike ImageExists, this does NOT fall back to Docker.
func ImageExistsInContainerd(ctx context.Context, image string, runner CommandRunner) bool {
	ref := image
	if !strings.Contains(ref, ":") {
		ref += ":latest"
	}
	out, err := runCrictl(ctx, runner, "images", "--quiet", ref)
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// ImageExistsInDocker checks whether image exists in Docker store.
func ImageExistsInDocker(ctx context.Context, image string, runner CommandRunner) bool {
	ref := image
	if !strings.Contains(ref, ":") {
		ref += ":latest"
	}
	out, err := runner.Run(ctx, "docker", "images", "-q", ref)
	return err == nil && len(strings.TrimSpace(string(out))) > 0
}

// ImportDockerToContainerd transfers an image from Docker store to K3S containerd.
// Uses runner.Pipe to stream docker save stdout into k3s ctr import stdin.
// Requires root privileges (containerd socket is root-owned).
func ImportDockerToContainerd(ctx context.Context, image string, runner CommandRunner) error {
	err := runner.Pipe(ctx,
		[]string{"docker", "save", image},
		[]string{"k3s", "ctr", "-n", "k8s.io", "images", "import", "-"},
	)
	if err != nil {
		return fmt.Errorf("import %s: %w", image, err)
	}
	return nil
}

// verifyDigest checks the pulled image's digest against an expected value.
// On mismatch or inspection failure it logs a warning but never returns an error
// (graceful degradation -- digest verification is advisory).
func verifyDigest(ctx context.Context, runner CommandRunner, ref, expectedDigest string) {
	if expectedDigest == "" {
		return
	}

	// Try docker inspect first.
	out, err := runner.Run(ctx, "docker", "inspect", "--format", "{{json .RepoDigests}}", ref)
	if err == nil {
		var digests []string
		if jsonErr := json.Unmarshal(out, &digests); jsonErr == nil {
			for _, d := range digests {
				// Each entry looks like "registry/image@sha256:abc123..."
				if idx := strings.Index(d, "@"); idx >= 0 {
					actual := d[idx+1:]
					if actual == expectedDigest {
						slog.Info("image digest verified", "ref", ref, "digest", expectedDigest)
						return
					}
				}
			}
		}
	}

	// Try crictl inspecti as fallback.
	out, err = runCrictl(ctx, runner, "inspecti", ref)
	if err == nil {
		var info struct {
			Status struct {
				RepoDigests []string `json:"repoDigests"`
			} `json:"status"`
		}
		if jsonErr := json.Unmarshal(out, &info); jsonErr == nil {
			for _, d := range info.Status.RepoDigests {
				if idx := strings.Index(d, "@"); idx >= 0 {
					actual := d[idx+1:]
					if actual == expectedDigest {
						slog.Info("image digest verified", "ref", ref, "digest", expectedDigest)
						return
					}
				}
			}
		}
	}

	slog.Warn("image digest verification: no matching digest found",
		"ref", ref, "expected", expectedDigest)
}

// buildImageRef constructs a full image reference from registry, image name, and tag.
// For registries that include a namespace (e.g., "registry.cn-hangzhou.aliyuncs.com/aima"),
// only the base image name is appended (not the full original path).
func buildImageRef(registry, image, tag string) string {
	// Extract just the base name from the image (e.g., "vllm-openai" from "vllm/vllm-openai")
	baseName := path.Base(image)

	// If registry already contains a path component (like "registry.cn/namespace"),
	// append just the base name. Otherwise, use the full image path.
	if strings.Contains(registry, "/") {
		return fmt.Sprintf("%s/%s:%s", registry, baseName, tag)
	}
	return fmt.Sprintf("%s/%s:%s", registry, image, tag)
}
