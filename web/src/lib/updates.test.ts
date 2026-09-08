import { describe, expect, it } from "vitest";
import {
  isTerminal,
  updateBadgeLabel,
  UPDATE_BLOCK_EXPLANATION,
  UPDATE_PHASE_LABEL,
  type FirmwareStatus,
  type UpdateOperationState,
} from "./updates";

describe("sidebar update badge", () => {
  it("names the version so the operator knows what the badge offers", () => {
    const status: FirmwareStatus = { update_available: true, target_version: "0.1.8" };
    expect(updateBadgeLabel(status)).toBe("New version · v0.1.8");
  });

  it("stays absent when there is nothing to install", () => {
    expect(updateBadgeLabel({ update_available: false, target_version: "0.1.8" })).toBeNull();
    expect(updateBadgeLabel(null)).toBeNull();
  });

  // A newer release that has no payload yet, or a failed check, must not raise
  // a badge that promises an install the appliance would refuse.
  it("stays absent when the server reports no installable target", () => {
    expect(updateBadgeLabel({ update_available: true })).toBeNull();
    expect(updateBadgeLabel({ check_error: "unavailable", update_available: false, latest_version: "0.1.9" })).toBeNull();
  });
});

describe("operation phases", () => {
  const running: UpdateOperationState[] = [
    "queued", "downloading", "verifying", "staging", "activating", "checking_health", "rolling_back",
  ];
  const finished: UpdateOperationState[] = ["succeeded", "failed", "rolled_back", "recovery_required"];

  it("treats only real outcomes as terminal", () => {
    running.forEach((state) => expect(isTerminal(state)).toBe(false));
    finished.forEach((state) => expect(isTerminal(state)).toBe(true));
    expect(isTerminal(undefined)).toBe(false);
  });

  it("has an operator-readable label for every phase the server can report", () => {
    [...running, ...finished].forEach((state) => {
      expect(UPDATE_PHASE_LABEL[state]).toBeTruthy();
    });
  });
});

describe("blocked reasons", () => {
  // The dashboard must be able to explain every refusal the server produces;
  // an unmapped code would surface as a silently disabled button.
  const serverCodes = [
    "missing_trust_key",
    "missing_update_helper",
    "missing_baseline",
    "unknown_installed_version",
    "below_installed_version",
    "pending_activation",
    "unsupported_architecture",
    "insufficient_space",
    "configuration_pending",
    "recovery_required",
    "update_in_progress",
    "check_unavailable",
    "no_candidate",
    "already_current",
    "local_state_unavailable",
    "candidate_superseded",
    "read_only_session",
  ];

  it("explains every code the API can return", () => {
    serverCodes.forEach((code) => {
      expect(UPDATE_BLOCK_EXPLANATION[code], `missing explanation for ${code}`).toBeTruthy();
    });
  });

  it("never tells the operator they are up to date when the check failed", () => {
    expect(UPDATE_BLOCK_EXPLANATION.check_unavailable).not.toMatch(/up to date|newest/i);
  });
});

import { claimUpdateReload, sameRunningVersion, createUpdateReloadTracker, observeUpdateReload } from "./updates";

describe("completed firmware reload", () => {
  it("requires a known running target and normalizes only the display prefix", () => {
    expect(sameRunningVersion("v0.1.8", "0.1.8")).toBe(true);
    expect(sameRunningVersion("v0.1.8-beta.1", "0.1.8")).toBe(false);
    expect(sameRunningVersion(undefined, "0.1.8")).toBe(false);
    expect(sameRunningVersion("0.1.7", "0.1.8")).toBe(false);
  });
  it("remembers the operation across reloads, while allowing the next update", () => {
    const values = new Map<string, string>();
    const storage = { getItem: (key: string) => values.get(key) ?? null, setItem: (key: string, value: string) => { values.set(key, value); } };
    expect(claimUpdateReload("first", "0.1.8", storage)).toBe(true);
    expect(claimUpdateReload("first", "v0.1.8", storage)).toBe(false);
    expect(claimUpdateReload("second", "0.1.9", storage)).toBe(true);
  });
  it("does not create an automatic reload loop when browser storage is denied", () => {
    const denied = { getItem: () => { throw new Error("denied"); }, setItem: () => {} };
    expect(claimUpdateReload("operation", "0.1.8", denied)).toBe(false);
  });
});


describe("current-document update reload evidence", () => {
  const status = (id: string, state: UpdateOperationState, running = "0.1.4", target = "0.1.5"): FirmwareStatus => ({
    running_version: running, operation: { id, state, from_version: "0.1.4", target_version: target },
  });
  it("ignores initial and repeated historical success, even with future timestamps", () => {
    const tracker = createUpdateReloadTracker();
    const historical = status("old", "succeeded", "v0.1.5");
    historical.operation!.completed_at = "2999-01-01T00:00:00Z";
    expect(observeUpdateReload(tracker, historical)).toBeNull();
    expect(observeUpdateReload(tracker, historical)).toBeNull();
  });
  it("requires the observed active operation to succeed on its actual running target", () => {
    const tracker = createUpdateReloadTracker();
    expect(observeUpdateReload(tracker, status("new", "activating"))).toBeNull();
    expect(observeUpdateReload(tracker, status("new", "succeeded"))).toBeNull();
    expect(observeUpdateReload(tracker, status("new", "succeeded", "v0.1.5"))).toEqual({ id: "new", target: "0.1.5" });
  });
  it("detects another-tab completion between polls and subsequent updates", () => {
    const tracker = createUpdateReloadTracker();
    expect(observeUpdateReload(tracker, status("old", "succeeded", "0.1.4", "0.1.4"))).toBeNull();
    expect(observeUpdateReload(tracker, status("new", "succeeded", "0.1.5"))).toEqual({ id: "new", target: "0.1.5" });
    expect(observeUpdateReload(tracker, status("next", "succeeded", "0.1.6", "0.1.6"))).toEqual({ id: "next", target: "0.1.6" });
  });
  it("never rearms an ignored historical identity after a newer success arrives", () => {
    const tracker = createUpdateReloadTracker();
    const old = status("old", "succeeded", "0.1.4", "0.1.4");
    expect(observeUpdateReload(tracker, old)).toBeNull();
    expect(observeUpdateReload(tracker, status("new", "succeeded", "0.1.6", "0.1.6"))).toEqual({ id: "new", target: "0.1.6" });
    expect(observeUpdateReload(tracker, old)).toBeNull();
    expect(tracker.running).toBe("0.1.6");
    expect(observeUpdateReload(tracker, status("next", "succeeded", "0.1.7", "0.1.7"))).toEqual({ id: "next", target: "0.1.7" });
  });
  it("does not interpret the first known version as a transition", () => {
    const tracker = createUpdateReloadTracker();
    expect(observeUpdateReload(tracker, {})).toBeNull();
    expect(observeUpdateReload(tracker, status("old", "succeeded", "0.1.5"))).toBeNull();
  });
  for (const outcome of ["failed", "rolling_back", "rolled_back", "recovery_required"] as const) {
    it(`never reloads for ${outcome} or a later replay of its success`, () => {
      const tracker = createUpdateReloadTracker();
      observeUpdateReload(tracker, status("new", "activating"));
      expect(observeUpdateReload(tracker, status("new", outcome, "0.1.5"))).toBeNull();
      expect(observeUpdateReload(tracker, status("new", "succeeded", "0.1.5"))).toBeNull();
    });
  }
});
