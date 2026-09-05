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
