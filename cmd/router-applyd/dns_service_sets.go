package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/services"
)

const maxServiceDestinations = 16384

// Apply the runtime limit here rather than changing the immutable router-setup
// generator; published v0.1.9 bootstrap binaries must remain byte-identical.
func boundServiceSets(candidate []byte) []byte {
	for service := range services.ServiceDomains {
		old := fmt.Sprintf("  set svc_%s { type ipv4_addr; flags timeout; timeout 4h; }", service)
		bounded := fmt.Sprintf("  set svc_%s { type ipv4_addr; flags timeout; timeout 4h; size %d; }", service, maxServiceDestinations)
		candidate = bytes.ReplaceAll(candidate, []byte(old), []byte(bounded))
	}
	return candidate
}

// Limit output while the process runs, not after allocating arbitrary output.
type limitedNftOutput struct{ bytes.Buffer }

func (b *limitedNftOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8<<20 {
		return 0, errors.New("nft state exceeds the 8 MiB safety limit")
	}
	return b.Buffer.Write(p)
}

var readNftServiceState = func() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/sbin/nft", "-j", "list", "table", "inet", "minimalrouter")
	cmd.Env = []string{"PATH=/sbin:/usr/sbin:/bin:/usr/bin", "LANG=C", "LC_ALL=C"}
	var output limitedNftOutput
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("read service destinations: %w", err)
	}
	return output.Bytes(), nil
}

// Only generated, allowlisted IPv4 service sets survive a table replacement.
// Addresses and remaining lifetimes are rendered as data into the same atomic
// delete/create batch. No rule text from the running table is replayed.
func preserveServiceDestinations(candidate []byte) ([]byte, error) {
	wanted := map[string]bool{}
	for service := range services.ServiceDomains {
		name := "svc_" + service
		if bytes.Contains(candidate, []byte("  set "+name+" { type ipv4_addr;")) {
			wanted[name] = true
		}
	}
	if len(wanted) == 0 {
		return nil, nil
	}
	started := time.Now()
	raw, err := readNftServiceState()
	if err != nil {
		return nil, err
	}
	return serviceDestinationBatch(raw, wanted, time.Since(started))
}

func serviceDestinationBatch(raw []byte, wanted map[string]bool, elapsed time.Duration) ([]byte, error) {
	var document struct {
		Nftables []struct {
			Set *struct {
				Family string `json:"family"`
				Table  string `json:"table"`
				Name   string `json:"name"`
				Type   string `json:"type"`
				Elem   []struct {
					Elem struct {
						Val     string `json:"val"`
						Expires int64  `json:"expires"`
					} `json:"elem"`
				} `json:"elem"`
			} `json:"set"`
		} `json:"nftables"`
	}
	if len(raw) > 8<<20 {
		return nil, errors.New("service destination snapshot is too large")
	}
	// Other nft set types may use scalar elements. Decode only requested sets.
	var envelope struct {
		Nftables []map[string]json.RawMessage `json:"nftables"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	for _, item := range envelope.Nftables {
		setRaw, ok := item["set"]
		if !ok {
			continue
		}
		var identity struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(setRaw, &identity); err != nil {
			return nil, err
		}
		if !wanted[identity.Name] {
			continue
		}
		if _, ok := services.ServiceDomains[strings.TrimPrefix(identity.Name, "svc_")]; !ok {
			return nil, errors.New("unknown service set")
		}
		wrapped := []byte(`{"nftables":[{"set":` + string(setRaw) + `}]}`)
		if err := json.Unmarshal(wrapped, &document); err != nil {
			return nil, err
		}
		s := document.Nftables[0].Set
		if s.Family != "inet" || s.Table != "minimalrouter" || s.Type != "ipv4_addr" || len(s.Elem) > maxServiceDestinations {
			return nil, errors.New("invalid service destination set")
		}
		values := make([]string, 0, len(s.Elem))
		for _, entry := range s.Elem {
			addr, err := netip.ParseAddr(entry.Elem.Val)
			if err != nil || !addr.Is4() || addr.IsUnspecified() || addr.IsMulticast() {
				return nil, errors.New("invalid service destination address")
			}
			// nft's JSON frontend exports whole seconds (json.c divides the
			// kernel's millisecond values by 1000).
			remaining := time.Duration(entry.Elem.Expires)*time.Second - elapsed
			if entry.Elem.Expires <= 0 || entry.Elem.Expires > int64((4*time.Hour)/time.Second) {
				continue
			}
			// Leave a second of headroom; entries never gain a fresh four hours.
			seconds := int64(remaining/time.Second) - 1
			if seconds > 0 {
				values = append(values, fmt.Sprintf("%s timeout %ds", addr.String(), seconds))
			}
		}
		if len(values) > 0 {
			sort.Strings(values)
			fmt.Fprintf(&out, "add element inet minimalrouter %s { %s }\n", s.Name, strings.Join(values, ", "))
		}
	}
	return out.Bytes(), nil
}
