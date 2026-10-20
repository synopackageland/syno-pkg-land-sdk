package compose

import "github.com/synopackageland/syno-pkg-land-sdk/internal/startupbudget"

// Startup limits apply to container readiness, independently of DSM worker
// acquisition and bounded daemon stop/uninstall operations.
const (
	DefaultStartupTimeoutSeconds = startupbudget.DefaultSeconds
	MaxStartupTimeoutSeconds     = startupbudget.MaxSeconds
	StartupBudgetEnv             = startupbudget.Environment
)
