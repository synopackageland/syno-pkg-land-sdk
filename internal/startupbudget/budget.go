// Package startupbudget holds the shared container startup contract.
package startupbudget

// The defaults must let a loaded HDD NAS with 2 GB RAM start an App. A single
// health probe on such a device can take far longer than on a workstation.
const (
	DefaultSeconds = 900
	MaxSeconds     = 900
	Environment    = "SYNOPKGLAND_STARTUP_BUDGET_SECONDS"

	// ProbeSeconds bounds one health request. Timeouts of a few seconds turn a
	// briefly stalled App into a failure.
	ProbeSeconds = 30
	// Floor for Compose healthcheck retries; more tolerant values are kept.
	// start_period is raised to the startup budget.
	HealthRetries = 3
)
