// Package runtimebudget defines the appliance's bounded startup contract.
// Keep this package dependency-free: bootstrap updates must not accidentally
// depend on management-plane implementation details.
package runtimebudget

import "time"

const (
	Reconcile      = 270 * time.Second
	Startup        = Reconcile + 30*time.Second
	ServiceCommand = Startup + 30*time.Second
)
