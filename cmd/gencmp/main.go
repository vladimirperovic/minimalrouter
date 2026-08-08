package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/vladimirperovic/minimalrouter/internal/config"
	"github.com/vladimirperovic/minimalrouter/internal/services"
)

func main() {
	data, err := os.ReadFile("/tmp/lastgood.json")
	if err != nil {
		fmt.Println("ERR read:", err)
		os.Exit(1)
	}
	var cfg config.SystemConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		fmt.Println("ERR parse:", err)
		os.Exit(1)
	}
	write := func(name string, s string) {
		os.WriteFile("/tmp/gen-"+name+".txt", []byte(s), 0644)
	}
	nft, _ := services.GenerateNftables(&cfg)
	write("nft", nft)
	pppoe, err := services.GeneratePPPoE(&cfg)
	if err != nil {
		fmt.Println("ERR pppoe:", err)
	} else {
		write("pppoe", pppoe.PeerConfig)
	}
	write("chap", pppoe.ChapSecrets)
	dns, _ := services.GenerateDnsmasq(&cfg)
	write("dnsmasq", dns)
	hostapd, _ := services.GenerateHostapd(&cfg)
	write("hostapd", hostapd)
	wg, _ := services.GenerateWireGuard(&cfg.WireGuard)
	write("wg", wg)
	wgr, _ := services.GenerateWireGuardRuntime(&cfg.WireGuard)
	write("wgruntime", wgr)
	wgc := "# WireGuard client disabled\n"
	if cfg.WGClient.Enabled {
		wgc, _ = services.GenerateWireGuardClientRuntime(&cfg.WGClient)
	}
	write("wgclientruntime", wgc)
	fmt.Println("OK all generated")
}
