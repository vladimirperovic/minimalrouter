package main

import (
	"errors"
	"strings"
	"testing"
)

func TestIFBDriverAutoloadCollisionIsVerified(t *testing.T) {
	const valid = `[{"ifname":"ifb0","linkinfo":{"info_kind":"ifb"}}]`
	for _, tc := range []struct {
		name, existing                       string
		firstMissing, createFails, wantError bool
	}{
		{"existing IFB", valid, false, false, false},
		{"driver creates default IFB", valid, true, true, false},
		{"new IFB", valid, true, false, false},
		{"foreign interface", `[{"ifname":"ifb0","linkinfo":{"info_kind":"dummy"}}]`, false, false, true},
		{"collision with foreign interface", `[{"ifname":"ifb0","linkinfo":{"info_kind":"dummy"}}]`, true, true, true},
		{"creation failed", "", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, creates := 0, 0
			err := ensureIFBInterface(func(binary string, args ...string) (string, error) {
				command := binary + " " + strings.Join(args, " ")
				if command == "/sbin/ip -j -d link show dev ifb0" {
					reads++
					if tc.firstMissing && reads == 1 {
						return "", errors.New("device unavailable")
					}
					return tc.existing, nil
				}
				if command == "/sbin/ip link add ifb0 type ifb" {
					creates++
					if tc.createFails {
						return "", errors.New("RTNETLINK: File exists")
					}
					return "", nil
				}
				t.Fatalf("unexpected privileged command %s", command)
				return "", nil
			})
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v", err)
			}
			if !tc.firstMissing && creates != 0 {
				t.Fatal("existing interface was modified")
			}
		})
	}
}
