package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"
)

// PPPoE recreates ppp0 on every reconnect, which removes its qdiscs. The PPP
// ip-up hook signals the already-running privileged helper instead of spawning
// a second router-applyd process. Reapplying under the same applyMu used by
// configuration transactions gives tc one process-wide serialization point:
// a reconnect can never race a normal apply and leave shaping from the wrong
// canonical revision active.
func init() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			applyMu.Lock()
			cfg, err := loadLastGood()
			if err != nil {
				log.Printf("reapply-qos signal: no last-good config: %v", err)
				applyMu.Unlock()
				continue
			}
			if cfg.QoS.Enabled {
				if err := applyQoS(*cfg); err != nil {
					log.Printf("reapply-qos signal: %v", err)
				}
			} else {
				clearQoS(*cfg)
			}
			applyMu.Unlock()
		}
	}()
}
