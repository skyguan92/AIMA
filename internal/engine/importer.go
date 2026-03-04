package engine

import (
	"context"
	"fmt"
	"os"
)

// Import loads a container image from an OCI tar file.
// Tries ctr (K3S containerd) first, falls back to docker load.
func Import(ctx context.Context, tarPath string, runner CommandRunner) error {
	if _, err := os.Stat(tarPath); err != nil {
		return fmt.Errorf("import image from %s: %w", tarPath, err)
	}

	// Try ctr (system containerd)
	if _, err := runner.Run(ctx, "ctr", "-n", "k8s.io", "images", "import", tarPath); err == nil {
		return nil
	}

	// Try k3s ctr (K3S bundled containerd)
	if _, err := runner.Run(ctx, "k3s", "ctr", "-n", "k8s.io", "images", "import", tarPath); err == nil {
		return nil
	}

	// Fallback to docker load
	if _, err := runner.Run(ctx, "docker", "load", "-i", tarPath); err == nil {
		return nil
	}

	return fmt.Errorf("import image from %s: none of ctr, k3s ctr, or docker succeeded", tarPath)
}
