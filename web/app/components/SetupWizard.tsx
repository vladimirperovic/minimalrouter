"use client";

import { useState } from "react";

interface SetupWizardProps {
  onComplete: () => void;
  onClose: () => void;
}

export default function SetupWizard({ onComplete, onClose }: SetupWizardProps) {
  const [step, setStep] = useState(1);
  const [wanIf, setWanIf] = useState("eth0");
  const [lanIf, setLanIf] = useState("eth1");
  const [pppoeUser, setPppoeUser] = useState("");
  const [pppoePass, setPppoePass] = useState("");
  const [adminPass, setAdminPass] = useState("");
  const [lanIP, setLanIP] = useState("192.168.1.1");
  const [errorMsg, setErrorMsg] = useState("");
  const [submitting, setSubmitting] = useState(false);

  const totalSteps = 5;

  const handleNext = () => {
    setErrorMsg("");
    if (step === 3 && (!pppoeUser || !pppoePass)) {
      setErrorMsg("Please enter both PPPoE username and password.");
      return;
    }
    if (step === 4) {
      if (adminPass.length < 15) {
        setErrorMsg("Administrator password must be at least 15 characters long.");
        return;
      }
    }
    if (step < totalSteps) {
      setStep(step + 1);
    }
  };

  const handlePrev = () => {
    setErrorMsg("");
    if (step > 1) {
      setStep(step - 1);
    }
  };

  const handleFinish = async () => {
    setSubmitting(true);
    setErrorMsg("");

    try {
      const res = await fetch("/api/v1/setup/apply", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          wan_interface: wanIf,
          lan_interface: lanIf,
          pppoe_username: pppoeUser,
          pppoe_password: pppoePass,
          admin_password: adminPass,
          lan_ip_address: lanIP,
        }),
      });

      if (!res.ok) {
        const text = await res.text();
        throw new Error(text || "Setup application failed");
      }

      setStep(6); // Success step
    } catch (err: any) {
      setErrorMsg(err.message || "Failed to apply configuration");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-backdrop" role="presentation">
      <div
        className="modal"
        style={{
          maxWidth: "640px",
          width: "100%",
          padding: "36px",
          borderRadius: "20px",
          background: "var(--surface)",
          boxShadow: "var(--shadow-raised)",
          border: "1px solid var(--separator)",
        }}
      >
        {step <= totalSteps && (
          <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "20px" }}>
            <span style={{ fontSize: "12px", fontWeight: 600, color: "var(--text-tertiary)", letterSpacing: "0.05em", textTransform: "uppercase" }}>
              Step {step} of {totalSteps}
            </span>
            <button
              onClick={onClose}
              style={{ border: "none", background: "none", fontSize: "20px", color: "var(--text-secondary)", cursor: "pointer" }}
            >
              ×
            </button>
          </div>
        )}

        {errorMsg && (
          <div
            style={{
              padding: "12px 16px",
              borderRadius: "10px",
              background: "var(--danger-soft)",
              color: "var(--danger)",
              fontSize: "13px",
              fontWeight: 500,
              marginBottom: "20px",
            }}
          >
            {errorMsg}
          </div>
        )}

        {/* STEP 1: WELCOME */}
        {step === 1 && (
          <div>
            <span className="eyebrow">Minimal Router OS</span>
            <h1 style={{ fontSize: "28px", fontWeight: 650, margin: "8px 0 12px", letterSpacing: "-0.02em" }}>
              Welcome to your new router.
            </h1>
            <p style={{ fontSize: "15px", color: "var(--text-secondary)", lineHeight: 1.55, marginBottom: "28px" }}>
              This wizard will guide you through setting up your PPPoE internet connection, LAN network, and administrator security in less than 2 minutes.
            </p>
            <div style={{ display: "flex", justifyContent: "flex-end" }}>
              <button className="button primary" type="button" onClick={handleNext}>
                Begin Setup →
              </button>
            </div>
          </div>
        )}

        {/* STEP 2: INTERFACE SELECTION */}
        {step === 2 && (
          <div>
            <span className="eyebrow">Network Interfaces</span>
            <h2 style={{ fontSize: "22px", fontWeight: 650, margin: "8px 0 16px" }}>Confirm WAN & LAN interfaces</h2>
            <div style={{ display: "flex", flexDirection: "column", gap: "16px", marginBottom: "28px" }}>
              <div>
                <label style={{ display: "block", fontSize: "13px", fontWeight: 600, marginBottom: "6px" }}>WAN Interface (Internet Port)</label>
                <select
                  value={wanIf}
                  onChange={(e) => setWanIf(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)", fontSize: "14px" }}
                >
                  <option value="eth0">eth0 (WAN Port)</option>
                  <option value="eth1">eth1</option>
                  <option value="em0">em0 (Intel NIC)</option>
                </select>
              </div>
              <div>
                <label style={{ display: "block", fontSize: "13px", fontWeight: 600, marginBottom: "6px" }}>LAN Interface (Local Network)</label>
                <select
                  value={lanIf}
                  onChange={(e) => setLanIf(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)", fontSize: "14px" }}
                >
                  <option value="eth1">eth1 (LAN Port)</option>
                  <option value="eth0">eth0</option>
                  <option value="em1">em1 (Intel NIC)</option>
                </select>
              </div>
            </div>
            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button className="button secondary" type="button" onClick={handlePrev}>Back</button>
              <button className="button primary" type="button" onClick={handleNext}>Continue →</button>
            </div>
          </div>
        )}

        {/* STEP 3: PPPoE CREDENTIALS */}
        {step === 3 && (
          <div>
            <span className="eyebrow">Internet Connection</span>
            <h2 style={{ fontSize: "22px", fontWeight: 650, margin: "8px 0 16px" }}>Enter PPPoE credentials</h2>
            <p style={{ fontSize: "14px", color: "var(--text-secondary)", marginBottom: "20px" }}>
              Provided by your Internet Service Provider (ISP).
            </p>
            <div style={{ display: "flex", flexDirection: "column", gap: "16px", marginBottom: "28px" }}>
              <div>
                <label style={{ display: "block", fontSize: "13px", fontWeight: 600, marginBottom: "6px" }}>PPPoE Username</label>
                <input
                  type="text"
                  placeholder="user@isp.net"
                  value={pppoeUser}
                  onChange={(e) => setPppoeUser(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)", fontSize: "14px" }}
                />
              </div>
              <div>
                <label style={{ display: "block", fontSize: "13px", fontWeight: 600, marginBottom: "6px" }}>PPPoE Password</label>
                <input
                  type="password"
                  placeholder="••••••••••••"
                  value={pppoePass}
                  onChange={(e) => setPppoePass(e.target.value)}
                  style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)", fontSize: "14px" }}
                />
              </div>
            </div>
            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button className="button secondary" type="button" onClick={handlePrev}>Back</button>
              <button className="button primary" type="button" onClick={handleNext}>Continue →</button>
            </div>
          </div>
        )}

        {/* STEP 4: ADMIN PASSWORD */}
        {step === 4 && (
          <div>
            <span className="eyebrow">Router Security</span>
            <h2 style={{ fontSize: "22px", fontWeight: 650, margin: "8px 0 16px" }}>Create administrator password</h2>
            <p style={{ fontSize: "14px", color: "var(--text-secondary)", marginBottom: "20px" }}>
              Minimum 15 characters required. Secured with Argon2id hashing.
            </p>
            <div style={{ marginBottom: "28px" }}>
              <label style={{ display: "block", fontSize: "13px", fontWeight: 600, marginBottom: "6px" }}>Administrator Password</label>
              <input
                type="password"
                placeholder="Minimum 15 characters"
                value={adminPass}
                onChange={(e) => setAdminPass(e.target.value)}
                style={{ width: "100%", padding: "10px 14px", borderRadius: "10px", border: "1px solid var(--separator)", background: "var(--surface)", fontSize: "14px" }}
              />
              <div style={{ fontSize: "12px", color: adminPass.length >= 15 ? "var(--success)" : "var(--text-tertiary)", marginTop: "6px" }}>
                {adminPass.length >= 15 ? "✓ Password meets 15+ character security requirement" : `${adminPass.length}/15 characters minimum`}
              </div>
            </div>
            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button className="button secondary" type="button" onClick={handlePrev}>Back</button>
              <button className="button primary" type="button" onClick={handleNext}>Review & Install →</button>
            </div>
          </div>
        )}

        {/* STEP 5: REVIEW */}
        {step === 5 && (
          <div>
            <span className="eyebrow">Review Setup</span>
            <h2 style={{ fontSize: "22px", fontWeight: 650, margin: "8px 0 16px" }}>Ready to apply configuration</h2>
            <div style={{ background: "var(--surface-muted)", padding: "18px", borderRadius: "12px", marginBottom: "28px", display: "flex", flexDirection: "column", gap: "12px", fontSize: "14px" }}>
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <span style={{ color: "var(--text-secondary)" }}>WAN Interface:</span>
                <strong>{wanIf} (PPPoE)</strong>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <span style={{ color: "var(--text-secondary)" }}>PPPoE User:</span>
                <strong>{pppoeUser}</strong>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <span style={{ color: "var(--text-secondary)" }}>LAN Gateway:</span>
                <code>{lanIP}/24 ({lanIf})</code>
              </div>
              <div style={{ display: "flex", justifyContent: "space-between" }}>
                <span style={{ color: "var(--text-secondary)" }}>DHCP Pool:</span>
                <code>192.168.1.100 – 192.168.1.200</code>
              </div>
            </div>
            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button className="button secondary" type="button" onClick={handlePrev} disabled={submitting}>Back</button>
              <button className="button primary" type="button" onClick={handleFinish} disabled={submitting}>
                {submitting ? "Applying Configuration..." : "Install & Connect"}
              </button>
            </div>
          </div>
        )}

        {/* STEP 6: SUCCESS */}
        {step === 6 && (
          <div style={{ textAlign: "center", padding: "12px 0" }}>
            <div style={{ width: "64px", height: "64px", borderRadius: "50%", background: "var(--success-soft)", color: "var(--success)", display: "grid", placeItems: "center", fontSize: "32px", margin: "0 auto 16px" }}>
              ✓
            </div>
            <span className="eyebrow">Setup Complete</span>
            <h1 style={{ fontSize: "28px", fontWeight: 650, margin: "8px 0 12px" }}>You are connected.</h1>
            <p style={{ fontSize: "15px", color: "var(--text-secondary)", marginBottom: "28px" }}>
              Minimal Router OS is active. Management dashboard is now secured at <code>https://{lanIP}</code>.
            </p>
            <button
              className="button primary"
              type="button"
              onClick={() => {
                onComplete();
                onClose();
              }}
            >
              Open Router Dashboard
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
