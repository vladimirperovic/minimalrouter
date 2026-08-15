import { useEffect, useMemo, useState } from "react";
import "./SetupWizard.css";

interface SetupWizardProps {
  onComplete: () => void;
  onClose?: () => void;
}

type InterfaceInfo = {
  name: string;
  mac_address?: string;
  up: boolean;
  carrier: boolean;
  physical: boolean;
  default_route: boolean;
  score: number;
};

type InterfaceDiscovery = {
  wan?: string;
  lan?: string;
  interfaces?: InterfaceInfo[];
  warnings?: string[];
};

const totalSteps = 5;
const stepTitles = ["Welcome", "Interfaces", "PPPoE", "Security", "Review"];

export default function SetupWizard({ onComplete, onClose }: SetupWizardProps) {
  const [step, setStep] = useState(1);
  const [wanIf, setWanIf] = useState("eth0");
  const [lanIf, setLanIf] = useState("eth1");
  const [interfaces, setInterfaces] = useState<InterfaceInfo[]>([]);
  const [interfaceWarnings, setInterfaceWarnings] = useState<string[]>([]);
  const [interfacesLoading, setInterfacesLoading] = useState(true);
  const [pppoeUser, setPppoeUser] = useState("");
  const [pppoePass, setPppoePass] = useState("");
  const [adminPass, setAdminPass] = useState("");
  const [adminPassConfirm, setAdminPassConfirm] = useState("");
  const [errorMsg, setErrorMsg] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const lanIP = "192.168.1.1";

  useEffect(() => {
    let active = true;
    const load = async () => {
      try {
        const [statusResponse, interfacesResponse] = await Promise.all([
          fetch("/api/v1/setup/status", { cache: "no-store", credentials: "same-origin" }),
          fetch("/api/v1/setup/interfaces", { cache: "no-store", credentials: "same-origin" }),
        ]);
        if (statusResponse.ok) {
          const status = await statusResponse.json();
          if (active && typeof status.wan_interface === "string") setWanIf(status.wan_interface);
          if (active && typeof status.lan_interface === "string") setLanIf(status.lan_interface);
        }
        if (interfacesResponse.ok) {
          const discovery = await interfacesResponse.json() as InterfaceDiscovery;
          if (!active) return;
          setInterfaces(Array.isArray(discovery.interfaces) ? discovery.interfaces : []);
          setInterfaceWarnings(Array.isArray(discovery.warnings) ? discovery.warnings : []);
          if (discovery.wan) setWanIf(discovery.wan);
          if (discovery.lan) setLanIf(discovery.lan);
        }
      } catch {
        if (active) {
          setInterfaceWarnings(["Automatic discovery is unavailable. Confirm the Linux interface names on the local console."]);
        }
      } finally {
        if (active) setInterfacesLoading(false);
      }
    };
    void load();
    return () => { active = false; };
  }, []);

  const options = useMemo<InterfaceInfo[]>(() => {
    if (interfaces.length > 0) return interfaces;
    return [
      { name: wanIf, up: false, carrier: false, physical: false, default_route: false, score: 0 },
      { name: lanIf, up: false, carrier: false, physical: false, default_route: false, score: 0 },
    ].filter((item, index, items) => items.findIndex((candidate) => candidate.name === item.name) === index);
  }, [interfaces, lanIf, wanIf]);

  const next = () => {
    setErrorMsg("");
    if (step === 2) {
      if (!wanIf || !lanIf) {
        setErrorMsg("Select both a WAN and a LAN interface.");
        return;
      }
      if (wanIf === lanIf) {
        setErrorMsg("WAN and LAN must be two different interfaces.");
        return;
      }
    }
    if (step === 3 && ((pppoeUser && !pppoePass) || (!pppoeUser && pppoePass))) {
      setErrorMsg("Enter both PPPoE credentials, or leave both fields empty.");
      return;
    }
    if (step === 4) {
      if (adminPass.length < 12) {
        setErrorMsg("The administrator password must be at least 12 characters.");
        return;
      }
      if (adminPass !== adminPassConfirm) {
        setErrorMsg("The administrator passwords do not match.");
        return;
      }
    }
    setStep((current) => Math.min(totalSteps, current + 1));
  };

  const previous = () => {
    setErrorMsg("");
    setStep((current) => Math.max(1, current - 1));
  };

  const finish = async () => {
    setSubmitting(true);
    setErrorMsg("");
    try {
      const response = await fetch("/api/v1/setup/apply", {
        method: "POST",
        credentials: "same-origin",
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
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || "Applying the configuration failed");
      }
      setPppoePass("");
      setAdminPass("");
      setAdminPassConfirm("");
      setStep(6);
    } catch (error) {
      setErrorMsg(error instanceof Error ? error.message : "Setup could not be applied");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <main className="setup-backdrop">
      <section aria-labelledby="setup-title" aria-modal="true" className="setup-panel" role="dialog">
        <header className="setup-header">
          <div className="setup-brand"><span aria-hidden="true">M</span><strong>Minimal Router OS</strong></div>
          {onClose && <button aria-label="Close setup" className="setup-close" onClick={onClose} type="button">✕</button>}
        </header>

        {step <= totalSteps && (
          <div className="setup-progress">
            <div className="setup-step-labels">
              {stepTitles.map((title, index) => <span className={index + 1 === step ? "is-current" : ""} key={title}>{index + 1}. {title}</span>)}
            </div>
            <progress aria-label="Setup progress" max={totalSteps} value={step} />
          </div>
        )}

        {errorMsg && <p className="setup-error" role="alert">{errorMsg}</p>}

        {step === 1 && (
          <div className="setup-page">
            <p className="eyebrow">First-run installation</p>
            <h1 id="setup-title">Welcome to your new router.</h1>
            <p className="setup-lead">This wizard sets the WAN and LAN roles, an optional PPPoE connection, and administrator protection. You can review every choice before anything is applied.</p>
            <div className="setup-feature-grid">
              <article><span>Safe apply</span><strong>Snapshot and rollback</strong><p>Disruptive changes are verified and can be returned to the previous state.</p></article>
              <article><span>Local recovery</span><strong>No default password</strong><p>The recovery console stays local and opens no network endpoint.</p></article>
            </div>
            <div className="setup-actions setup-actions-end"><button className="button primary" onClick={next} type="button">Start setup →</button></div>
          </div>
        )}

        {step === 2 && (
          <div className="setup-page">
            <p className="eyebrow">Network interfaces</p>
            <h1 id="setup-title">Confirm the WAN and LAN interfaces</h1>
            <p className="setup-lead">The recommendation uses the physical interface, link state and any existing default route. Nothing is applied without your confirmation.</p>
            {interfacesLoading ? <p className="setup-note">Discovering interfaces…</p> : null}
            {interfaceWarnings.map((warning) => <p className="setup-warning" key={warning}>{warning}</p>)}
            <div className="setup-interface-grid">
              <label className="field"><span>WAN — internet</span><select onChange={(event) => setWanIf(event.target.value)} value={wanIf}>{options.map((item) => <option key={`wan-${item.name}`} value={item.name}>{item.name}</option>)}</select></label>
              <label className="field"><span>LAN — local network</span><select onChange={(event) => setLanIf(event.target.value)} value={lanIf}>{options.map((item) => <option key={`lan-${item.name}`} value={item.name}>{item.name}</option>)}</select></label>
            </div>
            <div className="setup-interface-list">
              {options.map((item) => (
                <article className={(item.name === wanIf || item.name === lanIf) ? "is-selected" : ""} key={item.name}>
                  <div><strong>{item.name}</strong><code>{item.mac_address || "MAC unavailable"}</code></div>
                  <div className="setup-badges">
                    {item.physical && <span>Physical</span>}
                    {item.carrier && <span>Link</span>}
                    {item.default_route && <span>Default route</span>}
                    {item.name === wanIf && <span className="is-wan">WAN</span>}
                    {item.name === lanIf && <span className="is-lan">LAN</span>}
                  </div>
                </article>
              ))}
            </div>
            <p className="setup-note">LAN gateway after installation: <code>{lanIP}/24</code>. The LAN address can be changed safely later from the console.</p>
            <div className="setup-actions"><button className="button secondary" onClick={previous} type="button">Back</button><button className="button primary" onClick={next} type="button">Continue →</button></div>
          </div>
        )}

        {step === 3 && (
          <div className="setup-page">
            <p className="eyebrow">Internet connection</p>
            <h1 id="setup-title">Enter your PPPoE credentials</h1>
            <p className="setup-lead">Enter both credentials supplied by your ISP. For an isolated lab, leave both fields empty.</p>
            <div className="setup-fields">
              <label className="field"><span>PPPoE username</span><input autoComplete="username" onChange={(event) => setPppoeUser(event.target.value)} placeholder="user@isp.net" value={pppoeUser} /></label>
              <label className="field"><span>PPPoE password</span><input autoComplete="current-password" onChange={(event) => setPppoePass(event.target.value)} type="password" value={pppoePass} /></label>
            </div>
            <div className="setup-actions"><button className="button secondary" onClick={previous} type="button">Back</button><button className="button primary" onClick={next} type="button">Continue →</button></div>
          </div>
        )}

        {step === 4 && (
          <div className="setup-page">
            <p className="eyebrow">Router security</p>
            <h1 id="setup-title">Create the administrator password</h1>
            <p className="setup-lead">At least 12 characters. There is no factory password and no network fallback.</p>
            <div className="setup-fields">
              <label className="field"><span>Administrator password</span><input autoComplete="new-password" className={adminPass.length >= 12 ? "is-valid" : ""} onChange={(event) => setAdminPass(event.target.value)} type="password" value={adminPass} /></label>
              <label className="field"><span>Confirm password</span><input autoComplete="new-password" className={adminPassConfirm !== "" && adminPassConfirm === adminPass ? "is-valid" : ""} onChange={(event) => setAdminPassConfirm(event.target.value)} type="password" value={adminPassConfirm} /></label>
              <div className="setup-password-meter"><progress aria-label="Password minimum length" max={12} value={Math.min(adminPass.length, 12)} /><span>{adminPass.length >= 12 ? "✓ Minimum length met" : `${adminPass.length}/12 characters`}</span></div>
            </div>
            <div className="setup-actions"><button className="button secondary" onClick={previous} type="button">Back</button><button className="button primary" onClick={next} type="button">Review →</button></div>
          </div>
        )}

        {step === 5 && (
          <div className="setup-page">
            <p className="eyebrow">Final confirmation</p>
            <h1 id="setup-title">Review your setup</h1>
            <div className="setup-review-grid">
              <article><span>WAN</span><strong>{wanIf}</strong></article>
              <article><span>LAN</span><strong>{lanIf}</strong></article>
              <article><span>Internet</span><strong>{pppoeUser ? `PPPoE: ${pppoeUser}` : "PPPoE disabled"}</strong></article>
              <article><span>LAN gateway</span><strong>{lanIP}/24</strong></article>
              <article><span>DHCP</span><strong>192.168.1.100–192.168.1.200</strong></article>
              <article><span>Recovery</span><strong>Local console</strong></article>
            </div>
            <p className="setup-warning">Confirm the cabling matches the roles you selected. A wrong choice can cut off access until you repair it on the console.</p>
            <div className="setup-actions"><button className="button secondary" disabled={submitting} onClick={previous} type="button">Back</button><button className="button success" disabled={submitting} onClick={finish} type="button">{submitting ? "Applying…" : "Apply setup"}</button></div>
          </div>
        )}

        {step === 6 && (
          <div className="setup-page setup-success">
            <span aria-hidden="true" className="setup-success-icon">✓</span>
            <p className="eyebrow">Setup complete</p>
            <h1 id="setup-title">The router is initialised.</h1>
            <p className="setup-lead">Open the new management address and verify the displayed TLS fingerprint before signing in.</p>
            <code>https://{lanIP}:8443</code>
            <div className="setup-actions setup-actions-end"><button className="button primary" onClick={onComplete} type="button">Continue to sign-in</button></div>
          </div>
        )}
      </section>
    </main>
  );
}
