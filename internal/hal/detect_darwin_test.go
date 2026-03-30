//go:build darwin

package hal

import "testing"

func TestParseMemoryPressureAvailable(t *testing.T) {
	tests := []struct {
		name     string
		output   string
		totalMiB int
		wantMiB  int
		wantOK   bool
	}{
		{
			name: "standard output",
			output: "The system has 17179869184 (1048576 pages with a page size of 16384).\n\n" +
				"System-wide memory free percentage: 33%\n",
			totalMiB: 16384,
			wantMiB:  5407,
			wantOK:   true,
		},
		{
			name:     "missing summary line",
			output:   "Stats:\nPages free: 1000\n",
			totalMiB: 16384,
			wantMiB:  0,
			wantOK:   false,
		},
		{
			name:     "invalid percentage",
			output:   "System-wide memory free percentage: unknown\n",
			totalMiB: 16384,
			wantMiB:  0,
			wantOK:   false,
		},
		{
			name:     "missing total memory",
			output:   "System-wide memory free percentage: 25%\n",
			totalMiB: 0,
			wantMiB:  0,
			wantOK:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMiB, gotOK := parseMemoryPressureAvailable(tt.output, tt.totalMiB)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v", gotOK, tt.wantOK)
			}
			if gotMiB != tt.wantMiB {
				t.Fatalf("available = %d MiB, want %d MiB", gotMiB, tt.wantMiB)
			}
		})
	}
}

func TestParseVMStatAvailable(t *testing.T) {
	output := "" +
		"Mach Virtual Memory Statistics: (page size of 16384 bytes)\n" +
		"Pages free:                               262144.\n" +
		"Pages active:                             524288.\n" +
		"Pages inactive:                           131072.\n" +
		"Pages speculative:                         65536.\n" +
		"Pages purgeable:                           32768.\n" +
		"Pages throttled:                               0.\n"

	got := parseVMStatAvailable(output)
	if got != 7680 {
		t.Fatalf("available = %d MiB, want %d MiB", got, 7680)
	}
}

func TestDetectRAMPrefersMemoryPressure(t *testing.T) {
	runner := newMockRunner(map[string]mockResult{
		"sysctl -n hw.memsize": {
			output: []byte("17179869184\n"),
		},
		"memory_pressure": {
			output: []byte("System-wide memory free percentage: 25%\n"),
		},
		"vm_stat": {
			output: []byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 262144.\nPages inactive: 131072.\n"),
		},
		"sysctl vm.swapusage": {
			output: []byte("vm.swapusage: total = 25600.00M  used = 1024.00M  free = 24576.00M\n"),
		},
	})

	info := detectRAM(t.Context(), runner)
	if info.TotalMiB != 16384 {
		t.Fatalf("TotalMiB = %d, want %d", info.TotalMiB, 16384)
	}
	if info.AvailableMiB != 4096 {
		t.Fatalf("AvailableMiB = %d, want %d", info.AvailableMiB, 4096)
	}
	if info.SwapTotalMiB != 25600 {
		t.Fatalf("SwapTotalMiB = %d, want %d", info.SwapTotalMiB, 25600)
	}
}

func TestDetectRAMFallsBackToVMStat(t *testing.T) {
	runner := newMockRunner(map[string]mockResult{
		"sysctl -n hw.memsize": {
			output: []byte("17179869184\n"),
		},
		"vm_stat": {
			output: []byte("Mach Virtual Memory Statistics: (page size of 16384 bytes)\nPages free: 262144.\nPages inactive: 131072.\nPages speculative: 65536.\nPages purgeable: 32768.\n"),
		},
	})

	info := detectRAM(t.Context(), runner)
	if info.AvailableMiB != 7680 {
		t.Fatalf("AvailableMiB = %d, want %d", info.AvailableMiB, 7680)
	}
}
