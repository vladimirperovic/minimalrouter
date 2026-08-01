//go:build !linux

package health

import "github.com/vladimirperovic/minimalrouter/internal/config"

func inspectRuntimeFacts(config.SystemConfig) RuntimeFacts {
	return RuntimeFacts{Available: false}
}
