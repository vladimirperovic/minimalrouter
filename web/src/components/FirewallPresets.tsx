import { useState } from "react";
import {
  PRESETS,
  applyPresets,
  presetRules,
  presetState,
  type FirewallProps,
  type Preset,
} from "../lib/firewall";
export { PRESETS, ruleNameFor } from "../lib/firewall";

export default function FirewallPresets({
  config,
  busy,
  applyConfig,
}: FirewallProps) {
  const [selected, setSelected] = useState<string[]>([]);
  const rules = config.firewall.custom_rules || [];
  async function enable(presets: readonly Preset[]) {
    if (
      await applyConfig(
        (next) => applyPresets(next, presets),
        "Firewall presets applied.",
      )
    )
      setSelected((current) =>
        current.filter((id) => !presets.some((p) => p.id === id)),
      );
  }
  function disable(preset: Preset) {
    void applyConfig((next) => {
      const owned = new Set(
        presetRules(next.firewall.custom_rules || [], preset).map((r) => r.id),
      );
      next.firewall.custom_rules = next.firewall.custom_rules.map((r) =>
        owned.has(r.id) ? { ...r, enabled: false } : r,
      );
    }, "Preset paused.");
  }
  return (
    <section className="fw-panel" aria-labelledby="fw-presets-title">
      <header className="fw-panel-head">
        <div>
          <p className="fw-eyebrow">Choose your safeguards</p>
          <h3 id="fw-presets-title">Suggested rules</h3>
          <p>
            Optional restrictions for LAN → Internet traffic. Private tunnel
            routes stay outside these presets.
          </p>
        </div>
        <span className="fw-badge">
          {PRESETS.filter((p) => presetState(rules, p) === "Active").length} /{" "}
          {PRESETS.length} active
        </span>
      </header>
      <div className="fw-presets-grid">
        {PRESETS.map((preset) => {
          const state = presetState(rules, preset),
            checked = selected.includes(preset.id);
          return (
            <article
              className={`fw-preset ${state === "Active" ? "is-active" : ""} ${checked ? "is-selected" : ""}`}
              key={preset.id}
              aria-label={preset.title}
            >
              <div className="fw-preset-top">
                <span className="fw-preset-symbol" aria-hidden="true">
                  <svg
                    viewBox="0 0 24 24"
                    fill="none"
                    stroke="currentColor"
                    strokeWidth="1.4"
                  >
                    <rect x="3" y="4" width="7" height="7" rx="2" />
                    <path d="M6.5 11v6h9M13 4h7M13 8h5M17 14l3 3-3 3" />
                  </svg>
                </span>
                <span className="fw-eyebrow">{preset.group}</span>
                <span
                  className={`fw-badge ${state === "Active" ? "is-good" : state === "Modified" ? "is-warning" : ""}`}
                >
                  {state}
                </span>
              </div>
              <h4>{preset.title}</h4>
              <p>{preset.detail}</p>
              {preset.caution && <p className="fw-caution">{preset.caution}</p>}
              {state === "Modified" && (
                <p className="fw-caution">
                  The saved rule differs from this preset. Restore it to use the
                  match and WAN scope shown here.
                </p>
              )}
              <footer>
                <code>
                  {(preset.protocols || [preset.protocol])
                    .join(" + ")
                    .toUpperCase()}{" "}
                  · {preset.port}
                </code>
                {state === "Active" ? (
                  <button
                    className="button secondary small"
                    disabled={busy}
                    onClick={() => disable(preset)}
                  >
                    Turn off
                  </button>
                ) : (
                  <div className="fw-preset-actions">
                    <label>
                      <input
                        type="checkbox"
                        aria-label={`Select ${preset.title}`}
                        checked={checked}
                        disabled={busy}
                        onChange={(e) =>
                          setSelected((current) =>
                            e.target.checked
                              ? [...current, preset.id]
                              : current.filter((id) => id !== preset.id),
                          )
                        }
                      />
                      <span>Select</span>
                    </label>
                    <button
                      className="button secondary small"
                      disabled={busy || !checked}
                      onClick={() => void enable([preset])}
                    >
                      {state === "Modified" ? "Restore preset" : "Turn on"}
                    </button>
                  </div>
                )}
              </footer>
            </article>
          );
        })}
      </div>
      <div className="fw-batch">
        <div>
          <strong>
            {selected.length
              ? `${selected.length} selected`
              : "Apply several rules together"}
          </strong>
          <p>Review the whole batch before one configuration change.</p>
        </div>
        <button
          className="button primary"
          disabled={busy || !selected.length}
          onClick={() =>
            void enable(PRESETS.filter((p) => selected.includes(p.id)))
          }
        >
          Review selected rules
        </button>
      </div>
    </section>
  );
}
