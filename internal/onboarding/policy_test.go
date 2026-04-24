package onboarding

import "testing"

func TestParseFirstRunPolicyYAMLAllowsBooleanOverrides(t *testing.T) {
	policy, err := ParseFirstRunPolicyYAML([]byte(`
kind: onboarding_policy
first_run:
  native_guardrail:
    skip_discrete_accelerators: false
`))
	if err != nil {
		t.Fatalf("ParseFirstRunPolicyYAML: %v", err)
	}
	if policy.NativeGuardrail.SkipDiscreteAccelerators == nil {
		t.Fatal("skip_discrete_accelerators pointer is nil")
	}
	if *policy.NativeGuardrail.SkipDiscreteAccelerators {
		t.Fatal("skip_discrete_accelerators = true, want false")
	}
	if policy.NativeGuardrail.MaxPenalty == 0 {
		t.Fatal("expected defaults to fill max_penalty")
	}
}
