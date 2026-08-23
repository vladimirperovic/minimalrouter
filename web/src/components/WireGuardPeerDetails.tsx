import { useEffect, useRef } from "react";
import type { WireGuardPeer } from "../api-types";
import "./WireGuardPeerDetails.css";

// The peer row shows the two things an operator scans for — tunnel address and
// transfer. Everything else, including the learned endpoint that used to crowd
// the row, lives here behind "More info".

export type PeerLiveState = {
  public_key: string;
  endpoint?: string;
  allowed_ips?: string;
  last_handshake_epoch?: number;
  rx_bytes?: number;
  tx_bytes?: number;
  online: boolean;
};

export type PeerDetails = {
  peer: WireGuardPeer;
  live?: PeerLiveState;
  endpoint?: string;
  online: boolean;
  handshake?: number;
};

type Props = {
  details: PeerDetails | null;
  onClose: () => void;
};

function formatBytes(value = 0) {
  const units = ["B", "KB", "MB", "GB", "TB"];
  let amount = Math.max(0, value);
  let unit = 0;
  while (amount >= 1024 && unit < units.length - 1) {
    amount /= 1024;
    unit += 1;
  }
  return `${amount.toFixed(unit < 2 ? 0 : 1)} ${units[unit]}`;
}

function formatHandshake(epoch?: number) {
  if (!epoch) return "Never";
  const diff = Math.max(0, Date.now() / 1000 - epoch);
  const absolute = new Date(epoch * 1000).toLocaleString();
  if (diff < 60) return `Just now — ${absolute}`;
  const minutes = Math.floor(diff / 60);
  if (minutes < 60) return `${minutes} min ago — ${absolute}`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours} h ago — ${absolute}`;
  return absolute;
}

export default function WireGuardPeerDetails({ details, onClose }: Props) {
  const closeRef = useRef<HTMLButtonElement>(null);

  useEffect(() => {
    if (!details) return;
    closeRef.current?.focus();
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, [details, onClose]);

  if (!details) return null;

  const { peer, live, endpoint, online, handshake } = details;
  const state = !peer.enabled ? "Disabled" : online ? "Connected" : "Awaiting handshake";
  const tunnelIP = live?.allowed_ips || (peer.allowed_ips || []).join(", ") || "Not assigned";

  const rows: Array<{ label: string; value: string; mono?: boolean; hint?: string }> = [
    { label: "State", value: state },
    { label: "Last handshake", value: formatHandshake(handshake), hint: peer.enabled && !online ? "The peer has not completed a handshake yet. It appears once the device connects." : undefined },
    { label: "Tunnel IP", value: tunnelIP, mono: true, hint: "The address this device holds inside the tunnel." },
    { label: "Endpoint", value: endpoint || "Not learned", mono: true, hint: "The public address and port the peer last contacted the router from. It is learned from traffic, so it stays empty until the device connects and can change when the device moves network." },
    { label: "Public key", value: peer.public_key, mono: true, hint: "Identifies the peer. Safe to share; the private key never leaves the device." },
    { label: "Preshared key", value: peer.preshared_key ? "Configured" : "Not used", hint: "An optional extra symmetric secret layered on top of the key exchange." },
    { label: "Received", value: formatBytes(live?.rx_bytes || 0), mono: true },
    { label: "Sent", value: formatBytes(live?.tx_bytes || 0), mono: true },
  ];

  return (
    <div
      className="wg-details-backdrop"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget) onClose();
      }}
    >
      <div aria-labelledby="wg-details-title" aria-modal="true" className="wg-details-dialog" role="dialog">
        <header className="wg-details-header">
          <div>
            <span className="wg-details-kicker">Remote device</span>
            <h3 id="wg-details-title">{peer.name}</h3>
          </div>
          <button aria-label="Close device details" className="wg-details-close" onClick={onClose} ref={closeRef} type="button">×</button>
        </header>

        <dl className="wg-details-list">
          {rows.map((row) => (
            <div key={row.label}>
              <dt>{row.label}</dt>
              <dd className={row.mono ? "is-mono" : undefined}>{row.value}</dd>
              {row.hint && <p>{row.hint}</p>}
            </div>
          ))}
        </dl>

        <footer className="wg-details-footer">
          <button className="button secondary" onClick={onClose} type="button">Close</button>
        </footer>
      </div>
    </div>
  );
}
