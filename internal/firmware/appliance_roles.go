package firmware

import (
	"fmt"
	"os"
	"strings"
)

// ApplianceFileRole is compiled policy, never manifest-controlled root routing.
// Required describes staging completeness; SystemPath separately describes the
// stricter installed integration/rollback check. Installer-only files do not
// become arbitrary writable runtime destinations.
type ApplianceFileRole struct {
	Path               string
	SystemPath         string
	Mode               os.FileMode
	Required           bool
	ArchitectureBinary bool
	Bootstrap          bool
}

var supportedArchitectures = [...]string{"amd64", "arm64"}

var applianceFileRoles = [...]ApplianceFileRole{
	{Path: "web/dist/index.html", Mode: 0644, Required: true},
	{Path: "compatibility.json", SystemPath: "/etc/minimalrouter/compatibility.json", Mode: 0644, Required: true},
	{Path: "slot-exec", SystemPath: "/usr/libexec/minimalrouter/slot-exec", Mode: 0755, Required: true},
	{Path: "firstboot", SystemPath: "/usr/libexec/minimalrouter/firstboot", Mode: 0755, Required: true},
	{Path: "init.d/minimalrouter-firstboot", SystemPath: "/etc/init.d/minimalrouter-firstboot", Mode: 0755, Required: true},
	{Path: "firstboot-ready", SystemPath: "/usr/libexec/minimalrouter/firstboot-ready", Mode: 0755, Required: true},
	{Path: "install.sh", Mode: 0755, Required: true},
	{Path: "install-core.sh", Mode: 0755, Required: true},
	{Path: "init.d/routerd", SystemPath: "/etc/init.d/routerd", Mode: 0755, Required: true},
	{Path: "init.d/router-applyd", SystemPath: "/etc/init.d/router-applyd", Mode: 0755, Required: true},
	{Path: "init.d/pppoe-wan", SystemPath: "/etc/init.d/pppoe-wan", Mode: 0755, Required: true},
	{Path: "sysctl/99-minimalrouter.conf", SystemPath: "/etc/sysctl.d/99-minimalrouter.conf", Mode: 0644, Required: true},
	{Path: "modules/minimalrouter.conf", SystemPath: "/etc/modules-load.d/minimalrouter.conf", Mode: 0644, Required: true},
	{Path: "logrotate/minimalrouter", SystemPath: "/etc/logrotate.d/minimalrouter", Mode: 0644, Required: true},
	{Path: "ip-up.d-minimalrouter-qos", SystemPath: "/etc/ppp/ip-up.d/minimalrouter-qos", Mode: 0755, Required: true},
	{Path: "bin/routerd-{arch}", Mode: 0755, Required: true, ArchitectureBinary: true},
	{Path: "bin/router-applyd-{arch}", Mode: 0755, Required: true, ArchitectureBinary: true},
	// Recovery self-execs its database worker after permanently dropping to
	// routerd with no supplementary groups. CLI authority is checked in Go.
	{Path: "bin/router-recovery-{arch}", SystemPath: "/usr/libexec/minimalrouter/bootstrap/bin/router-recovery-{arch}", Mode: 0755, Required: true, ArchitectureBinary: true, Bootstrap: true},
	{Path: "bin/router-update-{arch}", SystemPath: "/usr/libexec/minimalrouter/bootstrap/bin/router-update-{arch}", Mode: 0750, Required: true, ArchitectureBinary: true, Bootstrap: true},
	{Path: "bin/router-setup-{arch}", SystemPath: "/usr/sbin/router-setup", Mode: 0755, Required: true, ArchitectureBinary: true, Bootstrap: true},
}

// ApplianceFileRoles returns an independent resolved copy of the static policy.
func ApplianceFileRoles(arch string) ([]ApplianceFileRole, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("unsupported update architecture %q", arch)
	}
	roles := make([]ApplianceFileRole, len(applianceFileRoles))
	for i, role := range applianceFileRoles {
		role.Path = strings.ReplaceAll(role.Path, "{arch}", arch)
		role.SystemPath = strings.ReplaceAll(role.SystemPath, "{arch}", arch)
		roles[i] = role
	}
	return roles, nil
}
