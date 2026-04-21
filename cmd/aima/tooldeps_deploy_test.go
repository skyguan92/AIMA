package main

import (
	"testing"

	"github.com/jguan/aima/internal/proxy"
	"github.com/jguan/aima/internal/runtime"
)

func TestResolvedServedModelNameExpandsModelTemplate(t *testing.T) {
	got := resolvedServedModelName("GLM-4.1V-9B-Thinking-FP4", map[string]any{
		"served_model_name": "{{.ModelName}}",
	})
	if got != "GLM-4.1V-9B-Thinking-FP4" {
		t.Fatalf("resolvedServedModelName = %q, want model name", got)
	}
}

func TestDeploymentUpstreamModelIgnoresUnresolvedTemplateLabel(t *testing.T) {
	got := deploymentUpstreamModel(&runtime.DeploymentStatus{
		Labels: map[string]string{
			proxy.LabelServedModel: "{{.ModelName}}",
			"aima.dev/model":       "GLM-4.1V-9B-Thinking-FP4",
		},
	}, "")
	if got != "GLM-4.1V-9B-Thinking-FP4" {
		t.Fatalf("deploymentUpstreamModel = %q, want model label fallback", got)
	}
}
