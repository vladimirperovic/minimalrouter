package main

import "log"

// routerKernelModules mirrors packaging/alpine/minimalrouter.modules, which the
// installer hands to OpenRC's `modules` service. A test keeps the lists equal.
var routerKernelModules = []string{
	"nf_conntrack", "ppp_generic", "pppox", "pppoe", "wireguard", "bridge", "sch_cake", "sch_fq_codel", "ifb",
}

// loadKernelModules runs exactly one fixed argument vector; it is a variable
// only so tests can observe it without a kernel.
var loadKernelModules = func() error {
	return runFixed("/sbin/modprobe", append([]string{"-a"}, routerKernelModules...)...)
}

// ensureRouterKernelModules loads the router's kernel modules before component
// preflight. Golden images up to v0.1.9 did not enable the `modules` service in
// the boot runlevel, so on an appliance installed without PPPoE nothing ever
// loaded ppp_generic: /dev/ppp was missing and the pppd preflight rejected every
// later PPPoE configuration. Loading is idempotent and best effort; a module
// that is genuinely unavailable still fails the specific preflight that needs
// it, with that component's own error.
func ensureRouterKernelModules() {
	if err := loadKernelModules(); err != nil {
		log.Printf("kernel module preload incomplete: %v", err)
	}
}
