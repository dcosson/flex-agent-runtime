package direct

import (
	"github.com/dcosson/flex-agent-runtime/internal/sandbox/control"
)

// instanceTypeMapping defines a resource threshold and its corresponding
// EC2 instance type. The mapping is evaluated in order; the first entry
// whose CPU and memory thresholds are >= the requested values is selected.
type instanceTypeMapping struct {
	maxCPUs    float64 // maximum CPUs this tier handles
	maxMemMB   int     // maximum memory in MiB this tier handles
	instType   string  // EC2 instance type
	actualCPUs float64 // actual vCPUs of the instance type
	actualMem  int     // actual memory in MiB of the instance type
}

// instanceTypeMappings maps resource requests to EC2 instance types.
// Ordered from smallest to largest. The mapping selects the first type
// whose thresholds accommodate the requested resources.
//
// | CPU (approx) | Memory (approx) | Instance Type | Actual Specs      |
// |-------------|-----------------|---------------|-------------------|
// | 1-2 vCPU    | <= 4 GiB        | t3.medium     | 2 vCPU / 4 GiB   |
// | 2-4 vCPU    | <= 8 GiB        | t3.large      | 2 vCPU / 8 GiB   |
// | 4-8 vCPU    | <= 16 GiB       | t3.xlarge     | 4 vCPU / 16 GiB  |
// | 8-16 vCPU   | <= 32 GiB       | m5.2xlarge    | 8 vCPU / 32 GiB  |
// | 16-32 vCPU  | <= 64 GiB       | m5.4xlarge    | 16 vCPU / 64 GiB |
var instanceTypeMappings = []instanceTypeMapping{
	{maxCPUs: 2, maxMemMB: 4096, instType: "t3.medium", actualCPUs: 2, actualMem: 4096},
	{maxCPUs: 4, maxMemMB: 8192, instType: "t3.large", actualCPUs: 2, actualMem: 8192},
	{maxCPUs: 8, maxMemMB: 16384, instType: "t3.xlarge", actualCPUs: 4, actualMem: 16384},
	{maxCPUs: 16, maxMemMB: 32768, instType: "m5.2xlarge", actualCPUs: 8, actualMem: 32768},
	{maxCPUs: 32, maxMemMB: 65536, instType: "m5.4xlarge", actualCPUs: 16, actualMem: 65536},
}

// selectInstanceType maps a resource request to an appropriate EC2 instance
// type. If the resources are zero-valued, returns the provided default
// instance type. Returns ErrResourcesExceedMaximum if the requested
// resources exceed the largest mapped type.
func selectInstanceType(resources control.ResourceSpec, defaultType string) (string, error) {
	// Zero-valued resources: use the default
	if resources.CPUs == 0 && resources.MemMB == 0 {
		return defaultType, nil
	}

	for _, m := range instanceTypeMappings {
		cpuFits := resources.CPUs <= m.maxCPUs || resources.CPUs == 0
		memFits := resources.MemMB <= m.maxMemMB || resources.MemMB == 0
		if cpuFits && memFits {
			return m.instType, nil
		}
	}

	return "", ErrResourcesExceedMaximum
}
