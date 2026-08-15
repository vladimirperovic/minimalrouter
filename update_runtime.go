package main

import (
	"fmt"
	"io/ioutil"
	"strings"
)

func main() {
	path := "internal/telemetry/runtime_linux.go"
	content, _ := ioutil.ReadFile(path)
	str := string(content)

	if !strings.Contains(str, "\"os/exec\"") {
		str = strings.Replace(str, "import (", "import (\n\t\"os/exec\"\n\t\"time\"", 1)
	}

	activeWgFunc := `
func countActiveWireGuardPeers() int {
	cmd := exec.Command("doas", "/usr/bin/wg", "show", "wg0", "latest-handshakes")
	out, err := cmd.Output()
	if err != nil {
		return 0
	}

	active := 0
	now := time.Now().Unix()
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		parts := strings.Fields(line)
		if len(parts) == 2 {
			ts, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil && ts > 0 && now-ts < 180 {
				active++
			}
		}
	}
	return active
}
`
	if !strings.Contains(str, "func countActiveWireGuardPeers") {
		str = str + activeWgFunc
	}

	str = strings.Replace(str, "return status", "status.WireguardActivePeers = countActiveWireGuardPeers()\n\treturn status", 1)

	ioutil.WriteFile(path, []byte(str), 0644)
	fmt.Println("updated")
}
