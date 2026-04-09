package agent

import "testing"

func TestBenchmarkMetadataComplete(t *testing.T) {
	tests := []struct {
		name          string
		concurrency   int
		rounds        int
		totalRequests int
		wantComplete  bool
	}{
		{"all zeros", 0, 0, 0, false},
		{"only concurrency", 4, 0, 0, false},
		{"only rounds", 0, 2, 0, false},
		{"only requests", 0, 0, 10, false},
		{"all valid", 4, 2, 10, true},
		{"minimal valid", 1, 1, 1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := benchmarkMetadataComplete(tt.concurrency, tt.rounds, tt.totalRequests)
			if got != tt.wantComplete {
				t.Errorf("benchmarkMetadataComplete(%d, %d, %d) = %v, want %v",
					tt.concurrency, tt.rounds, tt.totalRequests, got, tt.wantComplete)
			}
		})
	}
}
