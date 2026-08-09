import type { Plugin } from "@opencode-ai/plugin"
import { readFileSync } from "node:fs"

const STATS_FILE = "/tmp/lab-stats.json"
const TITLE = "📊 lab status"
const INTERVAL_MS = 15000
const MAX_MESSAGES = 20

export const LabStatsStatus: Plugin = async ({ client }) => {
  let sessionID: string | null = null
  let messageCount = 0

  const ensureSession = async () => {
    const sessions = await client.session.list()
    const existing = (sessions.data ?? []).find((s) => s.title === TITLE)
    if (existing) {
      sessionID = existing.id
      // count existing messages so we recycle after ~MAX_MESSAGES
      const msgs = await client.session.messages({ path: { id: existing.id }, query: { limit: 1 } })
      const info = msgs.data ?? []
      messageCount = info.length > 0 ? MAX_MESSAGES - 1 : 0
      if (info.length === 0) messageCount = 0
      else if ((msgs.data as unknown as { total?: number })?.total) messageCount = (msgs.data as unknown as { total: number }).total
      return existing.id
    }
    const created = await client.session.create({ body: { title: TITLE } })
    sessionID = created.data.id
    messageCount = 0
    return created.data.id
  }

  const recycle = async () => {
    if (sessionID) {
      try { await client.session.delete({ path: { id: sessionID } }) } catch { /* ignore */ }
    }
    sessionID = null
    messageCount = 0
    const created = await client.session.create({ body: { title: TITLE } })
    sessionID = created.data.id
  }

  const push = async () => {
    try {
      const s = JSON.parse(readFileSync(STATS_FILE, "utf8"))
      const line = [
        `🕒 ${s.ts}  |  CPU ${s.cpu.pct}% (load ${s.cpu.load["1min"]})  |  🔥 ${s.cpu.temp_host_c}°C  |  NVMe ${s.nvme_host_c}°C`,
        `🧠 RAM ${s.mem.used_mb}/${s.mem.total_mb} MB  |  💾 LXC disk ${s.disk.used}  |  🖥 host ${s.host_disk?.root_used ?? "?"} (${s.host_disk?.root_size ?? "?"})  |  🌐 RX ${s.net_eth1.rx_mb}MB / TX ${s.net_eth1.tx_mb}MB`,
        `📊 danas: max 🔥 ${s.today?.max_temp_c ?? "?"}°C  |  max CPU ${s.today?.max_cpu_pct ?? "?"}%  |  max RAM ${s.today?.max_ram_mb ?? "?"}MB  |  🪙 ${s.today?.tokens_used ?? "?"} tok`,
        `🛠 ${s.scenario?.phase ? `${s.scenario.scenario} / ${s.scenario.phase}` : (s.scenario?.scenario ?? "?")}  |  📈 tokens: ${s.opencode.total_tokens}  |  ⏳ ${s.uptime}`,
        ...(s.alerts?.length ? s.alerts.map((a) => `⚠️ ${a}`) : []),
      ].join("\n")
      if (!sessionID) sessionID = await ensureSession()
      messageCount++
      if (messageCount >= MAX_MESSAGES) await recycle()
      await client.session.prompt({
        path: { id: sessionID },
        body: { noReply: true, parts: [{ type: "text", text: line }] },
      })
    } catch {
      // stats file may not exist yet; retry next tick
    }
  }

  setInterval(push, INTERVAL_MS)
  push()
  return {}
}
