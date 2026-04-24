package onboarding

import "gopkg.in/yaml.v3"

// FirstRunPolicy contains product policy for the read-only onboarding guide.
// The factory policy is loaded from catalog/onboarding-policy.yaml by cmd/aima;
// tests and alternate embeddings can inject a policy through Deps.
type FirstRunPolicy struct {
	NativeGuardrail NativeFirstRunGuardrail `yaml:"native_guardrail" json:"native_guardrail"`
}

type NativeFirstRunGuardrail struct {
	Disabled                 bool                 `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	WildcardGPUArch          string               `yaml:"wildcard_gpu_arch,omitempty" json:"wildcard_gpu_arch,omitempty"`
	SkipDiscreteAccelerators *bool                `yaml:"skip_discrete_accelerators,omitempty" json:"skip_discrete_accelerators,omitempty"`
	RAMUtilizationPenalties  []UtilizationPenalty `yaml:"ram_utilization_penalties,omitempty" json:"ram_utilization_penalties,omitempty"`
	ParameterCountPenalties  []ParameterPenalty   `yaml:"parameter_count_penalties,omitempty" json:"parameter_count_penalties,omitempty"`
	MaxPenalty               int                  `yaml:"max_penalty,omitempty" json:"max_penalty,omitempty"`
}

type UtilizationPenalty struct {
	Above   float64 `yaml:"above" json:"above"`
	Penalty int     `yaml:"penalty" json:"penalty"`
}

type ParameterPenalty struct {
	AboveBillion float64 `yaml:"above_billion" json:"above_billion"`
	Penalty      int     `yaml:"penalty" json:"penalty"`
}

func DefaultFirstRunPolicy() FirstRunPolicy {
	return FirstRunPolicy{
		NativeGuardrail: NativeFirstRunGuardrail{
			WildcardGPUArch:          "*",
			SkipDiscreteAccelerators: boolPtr(true),
			RAMUtilizationPenalties: []UtilizationPenalty{
				{Above: 1.00, Penalty: 35},
				{Above: 0.85, Penalty: 25},
				{Above: 0.70, Penalty: 15},
				{Above: 0.55, Penalty: 8},
			},
			ParameterCountPenalties: []ParameterPenalty{
				{AboveBillion: 32, Penalty: 20},
				{AboveBillion: 14, Penalty: 12},
				{AboveBillion: 8, Penalty: 6},
			},
			MaxPenalty: 45,
		},
	}
}

func ParseFirstRunPolicyYAML(data []byte) (*FirstRunPolicy, error) {
	var doc struct {
		Kind     string         `yaml:"kind"`
		FirstRun FirstRunPolicy `yaml:"first_run"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	policy := doc.FirstRun.withDefaults()
	return &policy, nil
}

func effectiveFirstRunPolicy(deps *Deps) FirstRunPolicy {
	if deps != nil && deps.FirstRunPolicy != nil {
		return deps.FirstRunPolicy.withDefaults()
	}
	return DefaultFirstRunPolicy()
}

func (p FirstRunPolicy) withDefaults() FirstRunPolicy {
	defaults := DefaultFirstRunPolicy()
	if p.NativeGuardrail.Disabled {
		defaults.NativeGuardrail.Disabled = true
	}
	if p.NativeGuardrail.WildcardGPUArch != "" {
		defaults.NativeGuardrail.WildcardGPUArch = p.NativeGuardrail.WildcardGPUArch
	}
	if p.NativeGuardrail.SkipDiscreteAccelerators != nil {
		defaults.NativeGuardrail.SkipDiscreteAccelerators = p.NativeGuardrail.SkipDiscreteAccelerators
	}
	if len(p.NativeGuardrail.RAMUtilizationPenalties) > 0 {
		defaults.NativeGuardrail.RAMUtilizationPenalties = p.NativeGuardrail.RAMUtilizationPenalties
	}
	if len(p.NativeGuardrail.ParameterCountPenalties) > 0 {
		defaults.NativeGuardrail.ParameterCountPenalties = p.NativeGuardrail.ParameterCountPenalties
	}
	if p.NativeGuardrail.MaxPenalty > 0 {
		defaults.NativeGuardrail.MaxPenalty = p.NativeGuardrail.MaxPenalty
	}
	return defaults
}

func boolPtr(v bool) *bool {
	return &v
}
