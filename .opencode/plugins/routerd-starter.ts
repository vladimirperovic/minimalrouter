import type { Plugin } from "@opencode-ai/plugin"

const ROUTERD = "/Users/vladimirperovic/Documents/minimalrouter/bin/routerd"
const LOG = "/Users/vladimirperovic/Documents/minimalrouter/data/routerd.log"

export const RouterdStarter: Plugin = async ({ $ }) => {
  const ensureRunning = async () => {
    try {
      const running = await $`pgrep -f ${ROUTERD} >/dev/null && echo up || echo down`
      if (running.stdout.toString().trim() === "up") return
      const cmd = `MINIMALROUTER_PREVIEW_MODE=1 MINIMALROUTER_PREVIEW_HTTP=1 MINIMALROUTER_ALLOW_LOOPBACK_PREVIEW=1 MINIMALROUTER_PREVIEW_LAN=1 MINIMALROUTER_PREVIEW_PORT=8444 MINIMALROUTER_WEB_DIR=/Users/vladimirperovic/Documents/minimalrouter/web/dist nohup ${ROUTERD} > ${LOG} 2>&1 &`
      await $`sh -c ${cmd}`
    } catch {
      // best-effort: never block opencode on routerd failures
    }
  }
  return {
    "server.connected": ensureRunning,
    "session.created": ensureRunning,
  }
}
