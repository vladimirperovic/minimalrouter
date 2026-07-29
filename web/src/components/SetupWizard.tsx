import { useEffect, useState } from "react";

interface SetupWizardProps {
  onComplete: () => void;
  onClose?: () => void;
}

export default function SetupWizard({ onComplete, onClose }: SetupWizardProps) {
  const [step, setStep] = useState(1);
  const [wanIf, setWanIf] = useState("eth0");
  const [lanIf, setLanIf] = useState("eth1");
  const [pppoeUser, setPppoeUser] = useState("");
  const [pppoePass, setPppoePass] = useState("");
  const [adminPass, setAdminPass] = useState("");
  const [adminPassConfirm, setAdminPassConfirm] = useState("");
  const lanIP = "192.168.1.1";
  const [errorMsg, setErrorMsg] = useState("");
  const [submitting, setSubmitting] = useState(false);
  useEffect(() => {
    fetch("/api/v1/setup/status")
      .then(async (response) => {
        if (!response.ok) throw new Error("status unavailable");
        return response.json();
      })
      .then((status) => {
        if (typeof status.wan_interface === "string") setWanIf(status.wan_interface);
        if (typeof status.lan_interface === "string") setLanIf(status.lan_interface);
      })
      .catch(() => {
        // Defaults remain editable when status cannot be read.
      });
  }, []);

  const totalSteps = 5;
  const stepTitles = ["Welcome", "Interfaces", "PPPoE", "Security", "Review"];

  const handleNext = () => {
    setErrorMsg("");
    if (step === 3 && ((pppoeUser && !pppoePass) || (!pppoeUser && pppoePass))) {
      setErrorMsg("Unesite oba PPPoE podatka ili ostavite oba polja prazna.");
      return;
    }
    if (step === 4) {
      if (adminPass.length < 15) {
        setErrorMsg("Administrator lozinka mora imati najmanje 15 karaktera.");
        return;
      }
      if (adminPass !== adminPassConfirm) {
        setErrorMsg("Administrator lozinke se ne poklapaju.");
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
        throw new Error(text || "Greška pri primjeni konfiguracije");
      }

      setStep(6); // Success step
    } catch (err) {
      const msg = err instanceof Error ? err.message : "Neuspešna primjena podešavanja";
      setErrorMsg(msg);
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div
      style={{
        position: "fixed",
        inset: 0,
        zIndex: 9999,
        display: "grid",
        placeItems: "center",
        backgroundColor: "rgba(0, 0, 0, 0.4)",
        backdropFilter: "blur(20px)",
        WebkitBackdropFilter: "blur(20px)",
        padding: "24px",
      }}
      role="presentation"
    >
      <div
        style={{
          maxWidth: "680px",
          width: "100%",
          borderRadius: "28px",
          background: "var(--surface, #FFFFFF)",
          color: "var(--text-primary, #1D1D1F)",
          padding: "40px",
          boxShadow: "0 20px 40px rgba(0, 0, 0, 0.12), 0 1px 3px rgba(0, 0, 0, 0.05)",
          border: "1px solid rgba(0, 0, 0, 0.08)",
          fontFamily: "-apple-system, BlinkMacSystemFont, 'SF Pro Display', 'Segoe UI', Roboto, sans-serif",
          position: "relative",
          overflow: "hidden",
        }}
      >
        {/* Top Header & Close button */}
        <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "28px" }}>
          <div style={{ display: "flex", alignItems: "center", gap: "10px" }}>
            <div style={{ width: "32px", height: "32px", borderRadius: "10px", background: "#0071E3", display: "grid", placeItems: "center", color: "#FFF", fontWeight: 700, fontSize: "14px" }}>
              M
            </div>
            <span style={{ fontSize: "13px", fontWeight: 600, color: "#6E6E73", letterSpacing: "0.02em", textTransform: "uppercase" }}>
              Minimal Router OS · First-Run Setup
            </span>
          </div>
          {onClose && (
            <button
              onClick={onClose}
              style={{
                border: "none",
                background: "#F5F5F7",
                width: "32px",
                height: "32px",
                borderRadius: "50%",
                fontSize: "18px",
                color: "#6E6E73",
                cursor: "pointer",
                display: "grid",
                placeItems: "center",
                lineHeight: 1,
              }}
            >
              ✕
            </button>
          )}
        </div>

        {/* Apple Stepper Track */}
        {step <= totalSteps && (
          <div style={{ marginBottom: "32px" }}>
            <div style={{ display: "flex", justifyContent: "space-between", marginBottom: "8px" }}>
              {stepTitles.map((title, i) => (
                <span
                  key={i}
                  style={{
                    fontSize: "11px",
                    fontWeight: i + 1 === step ? 600 : 500,
                    color: i + 1 === step ? "#0071E3" : i + 1 < step ? "#1D1D1F" : "#86868B",
                    letterSpacing: "0.02em",
                  }}
                >
                  {i + 1}. {title}
                </span>
              ))}
            </div>
            <div style={{ height: "4px", width: "100%", background: "#E5E5EA", borderRadius: "2px", overflow: "hidden" }}>
              <div
                style={{
                  height: "100%",
                  width: `${(step / totalSteps) * 100}%`,
                  background: "#0071E3",
                  borderRadius: "2px",
                  transition: "width 0.3s cubic-bezier(0.16, 1, 0.3, 1)",
                }}
              />
            </div>
          </div>
        )}

        {/* Error message card */}
        {errorMsg && (
          <div
            style={{
              padding: "14px 18px",
              borderRadius: "14px",
              background: "#FF3B3015",
              border: "1px solid #FF3B3030",
              color: "#FF3B30",
              fontSize: "14px",
              fontWeight: 500,
              marginBottom: "24px",
            }}
          >
            {errorMsg}
          </div>
        )}

        {/* STEP 1: WELCOME */}
        {step === 1 && (
          <div>
            <div style={{ fontSize: "11px", fontWeight: 600, color: "#0071E3", textTransform: "uppercase", letterSpacing: "0.06em", marginBottom: "6px" }}>
              Prva instalacija
            </div>
            <h1 style={{ fontSize: "32px", fontWeight: 600, margin: "0 0 12px", letterSpacing: "-0.03em", color: "#1D1D1F" }}>
              Dobrodošli u novi ruter.
            </h1>
            <p style={{ fontSize: "16px", color: "#6E6E73", lineHeight: 1.5, marginBottom: "32px" }}>
              Ovaj čarobnjak će vas voditi kroz podešavanje PPPoE internet veze, LAN mreže i administrator sigurnosti u manje od 2 minute.
            </p>

            <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: "16px", marginBottom: "36px" }}>
              <div style={{ padding: "20px", borderRadius: "18px", background: "#F5F5F7", border: "1px solid #E5E5EA" }}>
                <div style={{ fontSize: "12px", color: "#6E6E73", fontWeight: 600, textTransform: "uppercase", marginBottom: "6px" }}>Brzina & Sigurnost</div>
                <div style={{ fontSize: "18px", fontWeight: 600, color: "#1D1D1F" }}>Kernel Control Plane</div>
                <div style={{ fontSize: "13px", color: "#86868B", marginTop: "4px" }}>Pogonjen Alpine Linux-om i Go mikrokontrolerom</div>
              </div>
              <div style={{ padding: "20px", borderRadius: "18px", background: "#F5F5F7", border: "1px solid #E5E5EA" }}>
                <div style={{ fontSize: "12px", color: "#6E6E73", fontWeight: 600, textTransform: "uppercase", marginBottom: "6px" }}>Zaštita stanja</div>
                <div style={{ fontSize: "18px", fontWeight: 600, color: "#1D1D1F" }}>Automatski Snapshotti</div>
                <div style={{ fontSize: "13px", color: "#86868B", marginTop: "4px" }}>Svaka izmjena je reverzibilna sa 1-klik rollback-om</div>
              </div>
            </div>

            <div style={{ display: "flex", justifyContent: "flex-end" }}>
              <button
                onClick={handleNext}
                style={{
                  background: "#0071E3",
                  color: "#FFFFFF",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 28px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                  boxShadow: "0 4px 12px rgba(0, 113, 227, 0.25)",
                }}
              >
                Započni podešavanje →
              </button>
            </div>
          </div>
        )}

        {/* STEP 2: INTERFACES */}
        {step === 2 && (
          <div>
            <div style={{ fontSize: "11px", fontWeight: 600, color: "#0071E3", textTransform: "uppercase", letterSpacing: "0.06em", marginBottom: "6px" }}>
              Mrežni priključci
            </div>
            <h2 style={{ fontSize: "26px", fontWeight: 600, margin: "0 0 8px", letterSpacing: "-0.02em" }}>
              Potvrdite WAN i LAN interfejse
            </h2>
            <p style={{ fontSize: "15px", color: "#6E6E73", marginBottom: "20px" }}>
              Provjerite stvarna Linux imena interfejsa na lokalnoj konzoli. Čarobnjak ne nagađa hardver niti tvrdi da je internet veza aktivna.
            </p>

            <div style={{ display: "flex", flexDirection: "column", gap: "20px", marginBottom: "36px" }}>
              <div>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "8px" }}>
                  <label style={{ fontSize: "13px", fontWeight: 600, color: "#1D1D1F" }}>
                    WAN Port (Internet priključak)
                  </label>
                  <span style={{ fontSize: "11px", color: "#86868B" }}>Podrazumijevano: eth0</span>
                </div>
                <input
                  type="text"
                  value={wanIf}
                  onChange={(e) => setWanIf(e.target.value)}
                  style={{
                    width: "100%",
                    padding: "14px 16px",
                    borderRadius: "14px",
                    border: "1px solid #D2D2D7",
                    background: "#FFFFFF",
                    fontSize: "15px",
                    color: "#1D1D1F",
                    outline: "none",
                  }}
                />
              </div>

              <div>
                <div style={{ display: "flex", justifyContent: "space-between", alignItems: "center", marginBottom: "8px" }}>
                  <label style={{ fontSize: "13px", fontWeight: 600, color: "#1D1D1F" }}>
                    LAN Port (Lokalna mreža)
                  </label>
                  <span style={{ fontSize: "11px", fontWeight: 600, color: "#86868B" }}>
                    Automatski dodijeljen (192.168.1.1)
                  </span>
                </div>
                <input
                  type="text"
                  value={lanIf}
                  onChange={(e) => setLanIf(e.target.value)}
                  style={{
                    width: "100%",
                    padding: "14px 16px",
                    borderRadius: "14px",
                    border: "1px solid #D2D2D7",
                    background: "#FFFFFF",
                    fontSize: "15px",
                    color: "#1D1D1F",
                    outline: "none",
                  }}
                />
              </div>
            </div>

            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button
                onClick={handlePrev}
                style={{
                  background: "#F5F5F7",
                  color: "#1D1D1F",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 24px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Nazad
              </button>
              <button
                onClick={handleNext}
                style={{
                  background: "#0071E3",
                  color: "#FFFFFF",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 28px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Nastavi →
              </button>
            </div>
          </div>
        )}

        {/* STEP 3: PPPoE CREDENTIALS */}
        {step === 3 && (
          <div>
            <div style={{ fontSize: "11px", fontWeight: 600, color: "#0071E3", textTransform: "uppercase", letterSpacing: "0.06em", marginBottom: "6px" }}>
              Internet Konekcija
            </div>
            <h2 style={{ fontSize: "26px", fontWeight: 600, margin: "0 0 8px", letterSpacing: "-0.02em" }}>
              Unesite PPPoE podatke
            </h2>
            <p style={{ fontSize: "15px", color: "#6E6E73", marginBottom: "28px" }}>
              Unesite oba podatka koja ste dobili od ISP-a. Za laboratoriju bez PPPoE veze ostavite oba polja prazna.
            </p>

            <div style={{ display: "flex", flexDirection: "column", gap: "20px", marginBottom: "36px" }}>
              <div>
                <label style={{ display: "block", fontSize: "13px", fontWeight: 600, color: "#1D1D1F", marginBottom: "8px" }}>
                  PPPoE Korisničko ime
                </label>
                <input
                  type="text"
                  placeholder="korisnik@isp.net"
                  value={pppoeUser}
                  onChange={(e) => setPppoeUser(e.target.value)}
                  style={{
                    width: "100%",
                    padding: "14px 16px",
                    borderRadius: "14px",
                    border: "1px solid #D2D2D7",
                    background: "#FFFFFF",
                    fontSize: "15px",
                    color: "#1D1D1F",
                    outline: "none",
                  }}
                />
              </div>

              <div>
                <label style={{ display: "block", fontSize: "13px", fontWeight: 600, color: "#1D1D1F", marginBottom: "8px" }}>
                  PPPoE Lozinka
                </label>
                <input
                  type="password"
                  placeholder="••••••••••••"
                  value={pppoePass}
                  onChange={(e) => setPppoePass(e.target.value)}
                  style={{
                    width: "100%",
                    padding: "14px 16px",
                    borderRadius: "14px",
                    border: "1px solid #D2D2D7",
                    background: "#FFFFFF",
                    fontSize: "15px",
                    color: "#1D1D1F",
                    outline: "none",
                  }}
                />
              </div>
            </div>

            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button
                onClick={handlePrev}
                style={{
                  background: "#F5F5F7",
                  color: "#1D1D1F",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 24px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Nazad
              </button>
              <button
                onClick={handleNext}
                style={{
                  background: "#0071E3",
                  color: "#FFFFFF",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 28px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Nastavi →
              </button>
            </div>
          </div>
        )}

        {/* STEP 4: ADMIN PASSWORD */}
        {step === 4 && (
          <div>
            <div style={{ fontSize: "11px", fontWeight: 600, color: "#0071E3", textTransform: "uppercase", letterSpacing: "0.06em", marginBottom: "6px" }}>
              Sigurnost Rutera
            </div>
            <h2 style={{ fontSize: "26px", fontWeight: 600, margin: "0 0 8px", letterSpacing: "-0.02em" }}>
              Kreirajte administrator lozinku
            </h2>
            <p style={{ fontSize: "15px", color: "#6E6E73", marginBottom: "28px" }}>
              Zahtijeva se najmanje 15 karaktera uz Argon2id zaštitu.
            </p>

            <div style={{ marginBottom: "36px", display: "grid", gap: "16px" }}>
              <label style={{ display: "block", fontSize: "13px", fontWeight: 600, color: "#1D1D1F", marginBottom: "8px" }}>
                Administrator Lozinka
              </label>
              <input
                type="password"
                placeholder="Najmanje 15 karaktera"
                value={adminPass}
                onChange={(e) => setAdminPass(e.target.value)}
                style={{
                  width: "100%",
                  padding: "14px 16px",
                  borderRadius: "14px",
                  border: `1px solid ${adminPass.length >= 15 ? "#34C759" : "#D2D2D7"}`,
                  background: "#FFFFFF",
                  fontSize: "15px",
                  color: "#1D1D1F",
                  outline: "none",
                }}
              />
              <label style={{ display: "block", fontSize: "13px", fontWeight: 600, color: "#1D1D1F" }}>
                Potvrdite administrator lozinku
              </label>
              <input
                type="password"
                placeholder="Ponovite lozinku"
                value={adminPassConfirm}
                onChange={(e) => setAdminPassConfirm(e.target.value)}
                style={{
                  width: "100%",
                  padding: "14px 16px",
                  borderRadius: "14px",
                  border: `1px solid ${adminPassConfirm && adminPassConfirm === adminPass ? "#34C759" : "#D2D2D7"}`,
                  background: "#FFFFFF",
                  fontSize: "15px",
                  color: "#1D1D1F",
                  outline: "none",
                }}
              />
              <div style={{ display: "flex", alignItems: "center", gap: "6px", marginTop: "8px" }}>
                <div style={{ flex: 1, height: "4px", background: "#E5E5EA", borderRadius: "2px", overflow: "hidden" }}>
                  <div
                    style={{
                      height: "100%",
                      width: `${Math.min(100, (adminPass.length / 15) * 100)}%`,
                      background: adminPass.length >= 15 ? "#34C759" : "#FF9500",
                      transition: "width 0.2s ease",
                    }}
                  />
                </div>
                <span style={{ fontSize: "12px", fontWeight: 600, color: adminPass.length >= 15 ? "#34C759" : "#86868B" }}>
                  {adminPass.length >= 15 ? "✓ Lozinka je sigurna" : `${adminPass.length}/15 karaktera`}
                </span>
              </div>
            </div>

            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button
                onClick={handlePrev}
                style={{
                  background: "#F5F5F7",
                  color: "#1D1D1F",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 24px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Nazad
              </button>
              <button
                onClick={handleNext}
                style={{
                  background: "#0071E3",
                  color: "#FFFFFF",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 28px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Pregledaj & Instaliraj →
              </button>
            </div>
          </div>
        )}

        {/* STEP 5: REVIEW */}
        {step === 5 && (
          <div>
            <div style={{ fontSize: "11px", fontWeight: 600, color: "#0071E3", textTransform: "uppercase", letterSpacing: "0.06em", marginBottom: "6px" }}>
              Završna potvrda
            </div>
            <h2 style={{ fontSize: "26px", fontWeight: 600, margin: "0 0 16px", letterSpacing: "-0.02em" }}>
              Pregled podešavanja
            </h2>

            {/* Apple Card Review Grid */}
            <div style={{ background: "#F5F5F7", padding: "24px", borderRadius: "20px", border: "1px solid #E5E5EA", marginBottom: "32px", display: "grid", gridTemplateColumns: "1fr 1fr", gap: "20px" }}>
              <div>
                <span style={{ fontSize: "12px", color: "#6E6E73", fontWeight: 600, textTransform: "uppercase", display: "block", marginBottom: "4px" }}>
                  WAN Priključak
                </span>
                <strong style={{ fontSize: "16px", color: "#1D1D1F" }}>{wanIf} (PPPoE)</strong>
              </div>

              <div>
                <span style={{ fontSize: "12px", color: "#6E6E73", fontWeight: 600, textTransform: "uppercase", display: "block", marginBottom: "4px" }}>
                  PPPoE Korisnik
                </span>
                <strong style={{ fontSize: "16px", color: "#1D1D1F" }}>{pppoeUser}</strong>
              </div>

              <div style={{ borderTop: "1px solid #E5E5EA", paddingTop: "14px" }}>
                <span style={{ fontSize: "12px", color: "#6E6E73", fontWeight: 600, textTransform: "uppercase", display: "block", marginBottom: "4px" }}>
                  LAN Gateway
                </span>
                <code style={{ fontSize: "15px", color: "#0071E3", fontWeight: 600 }}>{lanIP}/24 ({lanIf})</code>
              </div>

              <div style={{ borderTop: "1px solid #E5E5EA", paddingTop: "14px" }}>
                <span style={{ fontSize: "12px", color: "#6E6E73", fontWeight: 600, textTransform: "uppercase", display: "block", marginBottom: "4px" }}>
                  DHCP Opseg
                </span>
                <code style={{ fontSize: "15px", color: "#1D1D1F" }}>192.168.1.100–200</code>
              </div>
            </div>

            <div style={{ display: "flex", justifyContent: "space-between" }}>
              <button
                onClick={handlePrev}
                disabled={submitting}
                style={{
                  background: "#F5F5F7",
                  color: "#1D1D1F",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 24px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                }}
              >
                Nazad
              </button>
              <button
                onClick={handleFinish}
                disabled={submitting}
                style={{
                  background: "#34C759",
                  color: "#FFFFFF",
                  border: "none",
                  borderRadius: "14px",
                  padding: "14px 32px",
                  fontSize: "15px",
                  fontWeight: 600,
                  cursor: "pointer",
                  boxShadow: "0 4px 14px rgba(52, 199, 89, 0.3)",
                }}
              >
                {submitting ? "Primjenjujem..." : "Primjeni & Pokreni Ruter"}
              </button>
            </div>
          </div>
        )}

        {/* STEP 6: SUCCESS */}
        {step === 6 && (
          <div style={{ textAlign: "center", padding: "20px 0" }}>
            <div style={{ width: "72px", height: "72px", borderRadius: "50%", background: "#34C75915", color: "#34C759", display: "grid", placeItems: "center", fontSize: "36px", margin: "0 auto 20px" }}>
              ✓
            </div>
            <div style={{ fontSize: "11px", fontWeight: 600, color: "#34C759", textTransform: "uppercase", letterSpacing: "0.06em", marginBottom: "6px" }}>
              Podešavanje Uspješno
            </div>
            <h1 style={{ fontSize: "32px", fontWeight: 600, margin: "0 0 12px", letterSpacing: "-0.03em", color: "#1D1D1F" }}>
              Vaš ruter je aktivan.
            </h1>
            <p style={{ fontSize: "16px", color: "#6E6E73", marginBottom: "32px", maxWidth: "440px", marginInline: "auto" }}>
              Minimal Router OS je sačuvao podešavanja i kreirao nulti snapshot. Dashboard je dostupan na <code>https://{lanIP}</code>.
            </p>
            <button
              onClick={() => {
                onComplete();
              }}
              style={{
                background: "#0071E3",
                color: "#FFFFFF",
                border: "none",
                borderRadius: "14px",
                padding: "14px 32px",
                fontSize: "15px",
                fontWeight: 600,
                cursor: "pointer",
                boxShadow: "0 4px 14px rgba(0, 113, 227, 0.3)",
              }}
            >
              Otvori Router Dashboard
            </button>
          </div>
        )}
      </div>
    </div>
  );
}
