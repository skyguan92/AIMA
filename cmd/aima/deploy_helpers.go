package main

import (
	"context"
	"fmt"
	"sort"
	"strings"

	state "github.com/jguan/aima/internal"
	"github.com/jguan/aima/internal/knowledge"
	"github.com/jguan/aima/internal/runtime"
)

// pickRuntimeForDeployment selects the runtime for a specific deployment based on
// the engine's runtime recommendation and available runtimes.
//
//	"native"    → nativeRt
//	"docker"    → dockerRt > nativeRt
//	"k3s"       → k3sRt > error
//	"container" → k3sRt > dockerRt (needs partition? k3s required)
//	"auto" / "" → defaultRt
func pickRuntimeForDeployment(recommendation string, k3sRt, dockerRt, nativeRt, defaultRt runtime.Runtime, hasPartition bool) (runtime.Runtime, error) {
	switch recommendation {
	case "native":
		return nativeRt, nil
	case "docker":
		if dockerRt != nil {
			return dockerRt, nil
		}
		return nativeRt, nil
	case "k3s":
		if k3sRt != nil {
			return k3sRt, nil
		}
		return nil, fmt.Errorf("K3S runtime required but not available. Run 'aima init --k3s' to install")
	case "container":
		if hasPartition {
			if k3sRt != nil {
				return k3sRt, nil
			}
			return nil, fmt.Errorf("GPU partitioning requires K3S. Run 'aima init --k3s' to install")
		}
		if k3sRt != nil {
			return k3sRt, nil
		}
		if dockerRt != nil {
			return dockerRt, nil
		}
		return nativeRt, nil
	default: // "auto" or ""
		return defaultRt, nil
	}
}

func findExistingDeployment(ctx context.Context, query string, rts ...runtime.Runtime) *runtime.DeploymentStatus {
	for _, r := range rts {
		if r == nil {
			continue
		}
		if status, err := r.Status(ctx, query); err == nil {
			return status
		}
	}
	for _, d := range listAllRuntimes(ctx, rts...) {
		if deploymentMatchesQuery(d, query) {
			return d
		}
	}
	return nil
}

type deployOptions struct {
	allowAutoPull bool
}

type deployOptionsKey struct{}

func withDeployAutoPull(ctx context.Context, allow bool) context.Context {
	return context.WithValue(ctx, deployOptionsKey{}, deployOptions{allowAutoPull: allow})
}

func deployAutoPullAllowed(ctx context.Context) bool {
	opts, ok := ctx.Value(deployOptionsKey{}).(deployOptions)
	if !ok {
		return true
	}
	return opts.allowAutoPull
}

func splitImageRef(ref string) (name, tag string) {
	// Find the last slash to isolate the image+tag portion from registry:port
	slashIdx := strings.LastIndex(ref, "/")
	afterSlash := ref
	if slashIdx >= 0 {
		afterSlash = ref[slashIdx+1:]
	}
	colonIdx := strings.LastIndex(afterSlash, ":")
	if colonIdx < 0 {
		return ref, "latest"
	}
	// colonIdx is relative to afterSlash; convert to absolute position
	absColon := colonIdx
	if slashIdx >= 0 {
		absColon = slashIdx + 1 + colonIdx
	}
	return ref[:absColon], ref[absColon+1:]
}

func stringInSliceFold(values []string, want string) bool {
	for _, v := range values {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

func imageSupportsPlatform(ea *knowledge.EngineAsset, platform string) bool {
	if ea == nil || ea.Image.Name == "" {
		return false
	}
	if platform == "" {
		return true
	}
	return len(ea.Image.Platforms) == 0 || stringInSliceFold(ea.Image.Platforms, platform)
}

func engineMatchesHardware(ea *knowledge.EngineAsset, hw knowledge.HardwareInfo) bool {
	if ea == nil {
		return false
	}
	arch := strings.TrimSpace(ea.Hardware.GPUArch)
	return arch == "" || arch == "*" || strings.EqualFold(arch, hw.GPUArch)
}

func engineSupportsPlatform(ea *knowledge.EngineAsset, platform string) bool {
	if ea == nil || platform == "" {
		return ea != nil
	}
	if ea.Source != nil && ea.Source.Supports(platform) {
		return true
	}
	return imageSupportsPlatform(ea, platform)
}

func engineCompatibleWithHost(ea *knowledge.EngineAsset, hw knowledge.HardwareInfo) bool {
	return engineMatchesHardware(ea, hw) && engineSupportsPlatform(ea, hw.Platform)
}

func preferredEngineRuntimeType(ea *knowledge.EngineAsset, platform string) string {
	if ea == nil {
		return "container"
	}

	recommendation := ea.Runtime.Default
	if platform != "" {
		if rec, ok := ea.Runtime.PlatformRecommendations[platform]; ok && rec != "" {
			recommendation = rec
		}
	}

	switch recommendation {
	case "native":
		if ea.Source != nil && (platform == "" || ea.Source.Supports(platform)) {
			return "native"
		}
		if imageSupportsPlatform(ea, platform) {
			return "container"
		}
	case "container":
		if imageSupportsPlatform(ea, platform) {
			return "container"
		}
		if ea.Source != nil && (platform == "" || ea.Source.Supports(platform)) {
			return "native"
		}
	}

	if ea.Source != nil && (platform == "" || ea.Source.Supports(platform)) {
		return "native"
	}
	if imageSupportsPlatform(ea, platform) {
		return "container"
	}
	return "container"
}

func requiresRootImportForK3S(inContainerd, inDocker, isRoot bool) bool {
	return inDocker && !inContainerd && !isRoot
}

func shouldFallbackToDockerRuntime(runtimeName string, hasPartition, inContainerd, inDocker, isRoot bool, dockerAvailable bool) bool {
	return runtimeName == "k3s" &&
		dockerAvailable &&
		!hasPartition &&
		requiresRootImportForK3S(inContainerd, inDocker, isRoot)
}

func k3sDockerImportHint(image string) string {
	return fmt.Sprintf("engine image %s exists in Docker but not in K3S containerd; import requires root (sudo docker save %s | sudo k3s ctr -n k8s.io images import -)", image, image)
}

func k3sDockerFallbackWarning(image string) string {
	return fmt.Sprintf("engine image %s is available in Docker but not K3S containerd; using Docker runtime because importing into containerd requires root", image)
}

func installedRuntimeTypesForEngine(installed []*state.Engine, engineName, engineType string) []string {
	keys := map[string]bool{
		strings.ToLower(engineName): true,
		strings.ToLower(engineType): true,
	}
	set := make(map[string]bool)
	for _, e := range installed {
		if e == nil {
			continue
		}
		if keys[strings.ToLower(e.ID)] || keys[strings.ToLower(e.Type)] {
			if e.RuntimeType != "" {
				set[e.RuntimeType] = true
			}
		}
	}
	runtimeTypes := make([]string, 0, len(set))
	for rt := range set {
		runtimeTypes = append(runtimeTypes, rt)
	}
	sort.Strings(runtimeTypes)
	return runtimeTypes
}

func defaultEngineAsset(cat *knowledge.Catalog, hw knowledge.HardwareInfo) *knowledge.EngineAsset {
	if cat == nil {
		return nil
	}
	if name := cat.DefaultEngine(); name != "" {
		if ea := cat.FindEngineByName(name, hw); engineCompatibleWithHost(ea, hw) {
			return ea
		}
	}
	for i := range cat.EngineAssets {
		ea := &cat.EngineAssets[i]
		if ea.Metadata.Default && engineCompatibleWithHost(ea, hw) {
			return ea
		}
	}
	for i := range cat.EngineAssets {
		ea := &cat.EngineAssets[i]
		if engineCompatibleWithHost(ea, hw) {
			return ea
		}
	}
	return nil
}
