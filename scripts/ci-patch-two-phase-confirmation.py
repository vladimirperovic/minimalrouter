from pathlib import Path


def replace_once(text: str, old: str, new: str, name: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{name}: expected exactly one occurrence, found {count}")
    return text.replace(old, new, 1)


ipc_path = Path("internal/apply/ipc.go")
ipc = ipc_path.read_text()
if "OpCommitConfirmed" not in ipc:
    ipc = replace_once(
        ipc,
        '''const (
\tOpApplyAll        OperationType = "APPLY_ALL"
\tOpConfirm         OperationType = "CONFIRM"
\tOpLoadNftables    OperationType = "LOAD_NFTABLES"
\tOpReloadService   OperationType = "RELOAD_SERVICE"
\tOpRestoreSnapshot OperationType = "RESTORE_SNAPSHOT"
)''',
        '''const (
\tOpApplyAll        OperationType = "APPLY_ALL"
\tOpConfirm         OperationType = "CONFIRM"
\tOpCommitConfirmed OperationType = "COMMIT_CONFIRMED"
\tOpReconcile       OperationType = "RECONCILE"
\tOpLoadNftables    OperationType = "LOAD_NFTABLES"
\tOpReloadService   OperationType = "RELOAD_SERVICE"
\tOpRestoreSnapshot OperationType = "RESTORE_SNAPSHOT"
)''',
        "operation constants",
    )
ipc_path.write_text(ipc)

outcome_path = Path("cmd/router-applyd/outcome.go")
outcome = outcome_path.read_text()
if "replayTransactionResponseWithOverride" not in outcome:
    outcome = replace_once(
        outcome,
        '''func replayTransactionResponse(id, configHash string, previous *transactionRecord, loadErr error) (*apply.ApplyResponse, bool) {
\tif loadErr != nil {
\t\tif errors.Is(loadErr, os.ErrNotExist) {
\t\t\treturn nil, false
\t\t}
\t\tresponse := recoveryFailure(id, "transaction journal could not be read; canonical reconciliation is required")
\t\treturn &response, true
\t}
\tif previous == nil {
\t\tresponse := recoveryFailure(id, "transaction journal returned no record; canonical reconciliation is required")
\t\treturn &response, true
\t}
\tif previous.ID != id {
\t\treturn nil, false
\t}
\tif previous.ConfigHash != configHash {
\t\tresponse := failure(id, "transaction ID was already used for different content", false)
\t\treturn &response, true
\t}
\tif previous.CompletedAt.IsZero() {
\t\tresponse := recoveryFailure(id, "privileged transaction outcome is incomplete; canonical reconciliation is required")
\t\treturn &response, true
\t}
\tresponse := previous.Response
\treturn &response, true
}''',
        '''func replayTransactionResponse(id, configHash string, previous *transactionRecord, loadErr error) (*apply.ApplyResponse, bool) {
\treturn replayTransactionResponseWithOverride(id, configHash, previous, loadErr, false)
}

func replayTransactionResponseWithOverride(id, configHash string, previous *transactionRecord, loadErr error, canonicalReconcile bool) (*apply.ApplyResponse, bool) {
\tif loadErr != nil {
\t\tif errors.Is(loadErr, os.ErrNotExist) || canonicalReconcile {
\t\t\treturn nil, false
\t\t}
\t\tresponse := recoveryFailure(id, "transaction journal could not be read; canonical reconciliation is required")
\t\treturn &response, true
\t}
\tif previous == nil {
\t\tif canonicalReconcile {
\t\t\treturn nil, false
\t\t}
\t\tresponse := recoveryFailure(id, "transaction journal returned no record; canonical reconciliation is required")
\t\treturn &response, true
\t}
\tif previous.ID != id {
\t\tunresolved := previous.CompletedAt.IsZero() || previous.Response.RecoveryRequired
\t\tif unresolved && !canonicalReconcile {
\t\t\tresponse := recoveryFailure(id, "a previous privileged transaction remains unresolved; canonical reconciliation is required")
\t\t\treturn &response, true
\t\t}
\t\treturn nil, false
\t}
\tif previous.ConfigHash != configHash {
\t\tresponse := failure(id, "transaction ID was already used for different content", false)
\t\treturn &response, true
\t}
\tif previous.CompletedAt.IsZero() {
\t\tresponse := recoveryFailure(id, "privileged transaction outcome is incomplete; canonical reconciliation is required")
\t\treturn &response, true
\t}
\tresponse := previous.Response
\treturn &response, true
}''',
        "journal replay override",
    )
outcome_path.write_text(outcome)

main_path = Path("cmd/router-applyd/main.go")
main = main_path.read_text()
if "apply.OpCommitConfirmed" not in main:
    main = replace_once(
        main,
        '''\tif req.Op != apply.OpApplyAll && req.Op != apply.OpConfirm {
\t\twriteResponse(conn, apply.ApplyResponse{ID: req.ID, Success: false, Error: "operation is not allowlisted"})
\t\treturn
\t}''',
        '''\tswitch req.Op {
\tcase apply.OpApplyAll, apply.OpConfirm, apply.OpCommitConfirmed, apply.OpReconcile:
\tdefault:
\t\twriteResponse(conn, apply.ApplyResponse{ID: req.ID, Success: false, Error: "operation is not allowlisted"})
\t\treturn
\t}''',
        "helper operation allowlist",
    )
    main = replace_once(
        main,
        '''\tif lastTransactionMemory != nil {
\t\tif replay, handled := replayTransactionResponse(req.ID, configHash, lastTransactionMemory, nil); handled {
\t\t\twriteResponse(conn, *replay)
\t\t\treturn
\t\t}
\t}
\tprevious, loadErr := loadLastTransaction()
\tif replay, handled := replayTransactionResponse(req.ID, configHash, previous, loadErr); handled {
\t\twriteResponse(conn, *replay)
\t\treturn
\t}''',
        '''\tcanonicalReconcile := req.Op == apply.OpReconcile
\tif lastTransactionMemory != nil {
\t\tif replay, handled := replayTransactionResponseWithOverride(req.ID, configHash, lastTransactionMemory, nil, canonicalReconcile); handled {
\t\t\twriteResponse(conn, *replay)
\t\t\treturn
\t\t}
\t}
\tprevious, loadErr := loadLastTransaction()
\tif replay, handled := replayTransactionResponseWithOverride(req.ID, configHash, previous, loadErr, canonicalReconcile); handled {
\t\twriteResponse(conn, *replay)
\t\treturn
\t}''',
        "helper replay boundary",
    )
    main = replace_once(
        main,
        '''\tvar resp apply.ApplyResponse
\tif req.Op == apply.OpConfirm {
\t\tresp = confirmApply(req)
\t} else {
\t\tresp = applyAll(req)
\t}''',
        '''\tvar resp apply.ApplyResponse
\tswitch req.Op {
\tcase apply.OpConfirm:
\t\tresp = confirmApply(req)
\tcase apply.OpCommitConfirmed:
\t\tresp = commitConfirmedApply(req)
\tdefault:
\t\tresp = applyAll(req)
\t}''',
        "helper operation dispatch",
    )
    main = replace_once(
        main,
        '''func confirmApply(req apply.ApplyRequest) apply.ApplyResponse {
\tif err := req.Config.Validate(); err != nil {
\t\treturn failure(req.ID, "confirmation configuration is invalid", false)
\t}
\tpending, err := loadPendingConfirmation()
\tif err != nil {
\t\treturn pendingLoadFailure(req.ID, err)
\t}
\thash, err := hashConfig(req.Config)
\tif err != nil || hash != pending.ConfigHash {
\t\treturn failure(req.ID, "confirmation does not match pending configuration", false)
\t}
\tif err := configureRuntimeLAN(req.Config); err != nil {
\t\treturn recoveryFailure(req.ID, "could not finalize LAN address; verified rollback is required")
\t}
\tif err := saveLastGood(req.Config); err != nil {
\t\treturn recoveryFailure(req.ID, "could not persist confirmed configuration; verified rollback is required")
\t}
\tif err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
\t\treturn recoveryFailure(req.ID, "could not clear pending confirmation; canonical reconciliation is required")
\t}
\tif err := verifyActive(req.Config); err != nil {
\t\treturn recoveryFailure(req.ID, "confirmed configuration verification failed; verified rollback is required")
\t}
\treturn apply.ApplyResponse{
\t\tID: req.ID, Success: true, Verified: true, Logs: "configuration confirmed",
\t}
}''',
        '''func confirmApply(req apply.ApplyRequest) apply.ApplyResponse {
\tif err := req.Config.Validate(); err != nil {
\t\treturn failure(req.ID, "confirmation configuration is invalid", false)
\t}
\tpending, err := loadPendingConfirmation()
\tif err != nil {
\t\treturn pendingLoadFailure(req.ID, err)
\t}
\thash, err := hashConfig(req.Config)
\tif err != nil || hash != pending.ConfigHash {
\t\treturn failure(req.ID, "confirmation does not match pending configuration", false)
\t}
\tif err := configureRuntimeLAN(req.Config); err != nil {
\t\treturn recoveryFailure(req.ID, "could not finalize LAN address; verified rollback is required")
\t}
\tif err := verifyActive(req.Config); err != nil {
\t\treturn recoveryFailure(req.ID, "confirmed runtime verification failed; verified rollback is required")
\t}
\treturn apply.ApplyResponse{
\t\tID: req.ID, Success: true, Verified: true, Logs: "runtime confirmation verified; canonical commit pending",
\t}
}

func commitConfirmedApply(req apply.ApplyRequest) apply.ApplyResponse {
\tif err := req.Config.Validate(); err != nil {
\t\treturn failure(req.ID, "confirmed commit configuration is invalid", false)
\t}
\tpending, err := loadPendingConfirmation()
\tif err != nil {
\t\treturn pendingLoadFailure(req.ID, err)
\t}
\thash, err := hashConfig(req.Config)
\tif err != nil || hash != pending.ConfigHash {
\t\treturn failure(req.ID, "confirmed commit does not match pending configuration", false)
\t}
\tif err := verifyActive(req.Config); err != nil {
\t\treturn recoveryFailure(req.ID, "confirmed runtime is no longer active; canonical reconciliation is required")
\t}
\tif err := saveLastGood(req.Config); err != nil {
\t\treturn recoveryFailure(req.ID, "could not persist canonical last-good configuration")
\t}
\tif err := os.Remove(pendingPath); err != nil && !errors.Is(err, os.ErrNotExist) {
\t\treturn recoveryFailure(req.ID, "could not clear pending confirmation; canonical reconciliation is required")
\t}
\treturn apply.ApplyResponse{
\t\tID: req.ID, Success: true, Verified: true, Logs: "confirmed configuration committed as canonical last-good",
\t}
}''',
        "two-phase helper confirmation",
    )
main_path.write_text(main)

state_path = Path("internal/apply/statemachine.go")
state = state_path.read_text()
if "canonicalCommitted" not in state:
    state = replace_once(
        state,
        '''type pendingChange struct {
\ttx               *Transaction
\tprevious         config.SystemConfig
\ttimer            *time.Timer
\trollbackAttempts int
}''',
        '''type pendingChange struct {
\ttx                 *Transaction
\tprevious           config.SystemConfig
\ttimer              *time.Timer
\trollbackAttempts   int
\tcanonicalCommitted bool
}''',
        "pending confirmation state",
    )
    state = replace_once(
        state,
        '''func (e *Engine) ConfirmTransaction(txID string) (*Transaction, error) {
\te.mu.Lock()
\tdefer e.mu.Unlock()
\tif e.pending == nil || e.pending.tx.ID != txID {
\t\treturn nil, fmt.Errorf("transaction is not awaiting confirmation")
\t}
\tpending := e.pending
\treq := ApplyRequest{ID: txID + "-confirm", Op: OpConfirm, Revision: pending.tx.Config.Revision, Config: pending.tx.Config}
\tctx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
\tresp, err := e.applyPrivileged(ctx, req)
\tcancel()
\tif err != nil {
\t\tpending.tx.CurrentState = StateRecoveryRequired
\t\tpending.tx.Error = fmt.Sprintf("privileged confirmation outcome is unknown; verified rollback or retry is required: %v", err)
\t\treturn pending.tx, fmt.Errorf("privileged confirmation failed: %w", err)
\t}
\tif !resp.Success || !resp.Verified {
\t\tpending.tx.CurrentState = StateRecoveryRequired
\t\tpending.tx.Error = "privileged confirmation failed; verified rollback or retry is required: " + resp.Error
\t\treturn pending.tx, fmt.Errorf("privileged confirmation failed: %s", resp.Error)
\t}
\tif e.store != nil {
\t\tif err := e.store.SaveConfig(pending.tx.Config); err != nil {
\t\t\tpending.tx.CurrentState = StateRecoveryRequired
\t\t\tpending.tx.Error = "privileged confirmation succeeded but canonical configuration could not be committed; retry confirmation or allow verified rollback"
\t\t\treturn pending.tx, fmt.Errorf("failed to commit confirmed configuration: %w", err)
\t\t}
\t}
\tnow := time.Now()
\tpending.tx.ConfirmedAt = &now
\tpending.tx.CurrentState = StateCommitted
\tpending.timer.Stop()
\te.currentConfig = pending.tx.Config
\te.pending = nil
\te.activeTx = pending.tx
\treturn pending.tx, nil
}''',
        '''func (e *Engine) ConfirmTransaction(txID string) (*Transaction, error) {
\te.mu.Lock()
\tdefer e.mu.Unlock()
\tif e.pending == nil || e.pending.tx.ID != txID {
\t\treturn nil, fmt.Errorf("transaction is not awaiting confirmation")
\t}
\tpending := e.pending
\tif !pending.canonicalCommitted {
\t\treq := ApplyRequest{ID: txID + "-confirm-runtime", Op: OpConfirm, Revision: pending.tx.Config.Revision, Config: pending.tx.Config}
\t\tctx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
\t\tresp, err := e.applyPrivileged(ctx, req)
\t\tcancel()
\t\tif err != nil {
\t\t\tpending.tx.CurrentState = StateRecoveryRequired
\t\t\tpending.tx.Error = fmt.Sprintf("privileged runtime confirmation outcome is unknown; verified rollback or retry is required: %v", err)
\t\t\te.requireRecovery(pending.tx.Error)
\t\t\treturn pending.tx, fmt.Errorf("privileged runtime confirmation failed: %w", err)
\t\t}
\t\tif !resp.Success || !resp.Verified {
\t\t\tpending.tx.CurrentState = StateRecoveryRequired
\t\t\tpending.tx.Error = "privileged runtime confirmation failed; verified rollback or retry is required: " + resp.Error
\t\t\te.requireRecovery(pending.tx.Error)
\t\t\treturn pending.tx, fmt.Errorf("privileged runtime confirmation failed: %s", resp.Error)
\t\t}
\t\tif e.store != nil {
\t\t\tif err := e.store.SaveConfig(pending.tx.Config); err != nil {
\t\t\t\tpending.tx.CurrentState = StateRecoveryRequired
\t\t\t\tpending.tx.Error = "runtime confirmation succeeded but canonical configuration could not be committed; retry confirmation or allow verified rollback"
\t\t\t\treturn pending.tx, fmt.Errorf("failed to commit confirmed configuration: %w", err)
\t\t\t}
\t\t}
\t\tpending.canonicalCommitted = true
\t\te.currentConfig = pending.tx.Config
\t\tif pending.timer != nil {
\t\t\tpending.timer.Stop()
\t\t}
\t}

\tcommitReq := ApplyRequest{ID: txID + "-commit-confirmed", Op: OpCommitConfirmed, Revision: pending.tx.Config.Revision, Config: pending.tx.Config}
\tcommitCtx, commitCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
\tcommitResp, commitErr := e.applyPrivileged(commitCtx, commitReq)
\tcommitCancel()
\tif commitErr != nil || !commitResp.Success || !commitResp.Verified {
\t\tpending.tx.CurrentState = StateRecoveryRequired
\t\tif commitErr != nil {
\t\t\tpending.tx.Error = fmt.Sprintf("canonical configuration was committed but helper last-good acknowledgement is unknown: %v", commitErr)
\t\t} else {
\t\t\tpending.tx.Error = "canonical configuration was committed but helper last-good acknowledgement failed: " + commitResp.Error
\t\t}
\t\te.requireRecovery(pending.tx.Error)
\t\treturn pending.tx, fmt.Errorf("confirmed helper commit failed")
\t}

\tnow := time.Now()
\tpending.tx.ConfirmedAt = &now
\tpending.tx.CurrentState = StateCommitted
\te.currentConfig = pending.tx.Config
\te.pending = nil
\te.activeTx = pending.tx
\te.recoveryRequired = false
\te.recoveryReason = ""
\treturn pending.tx, nil
}''',
        "two-phase engine confirmation",
    )
    state = replace_once(
        state,
        '''\tpending := e.pending
\tpending.rollbackAttempts++''',
        '''\tpending := e.pending
\tif pending.canonicalCommitted {
\t\treturn
\t}
\tpending.rollbackAttempts++''',
        "canonical rollback guard",
    )
    state = replace_once(
        state,
        '''\t\t\te.pending = nil
\t\t\treturn''',
        '''\t\t\te.pending = nil
\t\t\te.recoveryRequired = false
\t\t\te.recoveryReason = ""
\t\t\treturn''',
        "rollback recovery clear",
    )
    state = replace_once(
        state,
        '''\treq, err := buildApplyRequest(fmt.Sprintf("boot-reconcile-%d", time.Now().UnixNano()), e.currentConfig)
\tif err != nil {
\t\treturn err
\t}''',
        '''\treq, err := buildApplyRequest(fmt.Sprintf("boot-reconcile-%d", time.Now().UnixNano()), e.currentConfig)
\tif err != nil {
\t\treturn err
\t}
\treq.Op = OpReconcile''',
        "reconcile operation",
    )
state_path.write_text(state)

new_test = Path("internal/apply/two_phase_confirmation_test.go")
if not new_test.exists():
    new_test.write_text('''package apply

import (
\t"context"
\t"testing"

\t"github.com/vladimirperovic/minimalrouter/internal/config"
)

type confirmationOrderingClient struct {
\tstore     *config.FileStore
\tinitial   config.SystemConfig
\tcandidate config.SystemConfig
\trequests  []ApplyRequest
}

func (c *confirmationOrderingClient) Apply(_ context.Context, req ApplyRequest) (*ApplyResponse, error) {
\tc.requests = append(c.requests, req)
\tstored, err := c.store.GetLatestConfig()
\tif err != nil {
\t\treturn nil, err
\t}
\tswitch req.Op {
\tcase OpApplyAll, OpConfirm:
\t\tif stored.LAN.CIDR != c.initial.LAN.CIDR {
\t\t\ttestingError := "canonical store changed before runtime confirmation completed"
\t\t\treturn &ApplyResponse{ID: req.ID, Success: false, Error: testingError}, nil
\t\t}
\tcase OpCommitConfirmed:
\t\tif stored.LAN.CIDR != c.candidate.LAN.CIDR {
\t\t\treturn &ApplyResponse{ID: req.ID, Success: false, Error: "helper commit ran before canonical store commit"}, nil
\t\t}
\t}
\treturn &ApplyResponse{ID: req.ID, Success: true, Verified: true}, nil
}

func TestConfirmationCommitsCanonicalStoreBeforeHelperLastGood(t *testing.T) {
\tstore := newScenarioStore(t)
\tinitial, err := store.GetLatestConfig()
\tif err != nil {
\t\tt.Fatalf("read initial config: %v", err)
\t}
\tcandidate := candidateWithNewLAN(initial)
\tclient := &confirmationOrderingClient{store: store, initial: initial, candidate: candidate}
\tengine := NewEngineWithClient(initial, store, client)

\ttx, err := engine.ProcessTransaction("tx-two-phase-order", candidate)
\tif err != nil {
\t\tt.Fatalf("create pending transaction: %v", err)
\t}
\tconfirmed, err := engine.ConfirmTransaction(tx.ID)
\tif err != nil {
\t\tt.Fatalf("confirm transaction: %v", err)
\t}
\tif confirmed.CurrentState != StateCommitted {
\t\tt.Fatalf("confirmation state=%s", confirmed.CurrentState)
\t}
\twant := []OperationType{OpApplyAll, OpConfirm, OpCommitConfirmed}
\tif len(client.requests) != len(want) {
\t\tt.Fatalf("request count=%d, want %d", len(client.requests), len(want))
\t}
\tfor i, op := range want {
\t\tif client.requests[i].Op != op {
\t\t\tt.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
\t\t}
\t}
}

func TestHelperCommitFailureRetriesCommitWithoutRepeatingRuntimeConfirmation(t *testing.T) {
\tstore := newScenarioStore(t)
\tinitial, err := store.GetLatestConfig()
\tif err != nil {
\t\tt.Fatalf("read initial config: %v", err)
\t}
\tclient := &scenarioApplyClient{steps: []scenarioApplyStep{
\t\tsuccessfulScenarioStep(),
\t\tsuccessfulScenarioStep(),
\t\t{response: ApplyResponse{Success: false, RecoveryRequired: true, Error: "last-good storage unavailable"}},
\t\tsuccessfulScenarioStep(),
\t}}
\tengine := NewEngineWithClient(initial, store, client)
\ttx, err := engine.ProcessTransaction("tx-two-phase-retry", candidateWithNewLAN(initial))
\tif err != nil {
\t\tt.Fatalf("create pending transaction: %v", err)
\t}
\tif _, err := engine.ConfirmTransaction(tx.ID); err == nil {
\t\tt.Fatal("helper commit failure was accepted")
\t}
\tpending := engine.GetPendingTransaction()
\tif pending == nil || pending.CurrentState != StateRecoveryRequired {
\t\tt.Fatalf("helper commit failure lost pending recovery: %+v", pending)
\t}
\tconfirmed, err := engine.ConfirmTransaction(tx.ID)
\tif err != nil {
\t\tt.Fatalf("retry helper commit: %v", err)
\t}
\tif confirmed.CurrentState != StateCommitted {
\t\tt.Fatalf("retry state=%s", confirmed.CurrentState)
\t}
\twant := []OperationType{OpApplyAll, OpConfirm, OpCommitConfirmed, OpCommitConfirmed}
\tif len(client.requests) != len(want) {
\t\tt.Fatalf("request count=%d, want %d", len(client.requests), len(want))
\t}
\tfor i, op := range want {
\t\tif client.requests[i].Op != op {
\t\t\tt.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
\t\t}
\t}
}
''')

helper_test = Path("cmd/router-applyd/reconcile_override_test.go")
if not helper_test.exists():
    helper_test.write_text('''package main

import (
\t"errors"
\t"testing"
\t"time"

\t"github.com/vladimirperovic/minimalrouter/internal/apply"
)

func TestUnresolvedPreviousTransactionBlocksNewMutationButAllowsReconcile(t *testing.T) {
\trecord := validTransactionRecordForTest()
\trecord.Response = apply.ApplyResponse{}
\trecord.CompletedAt = time.Time{}

\tblocked, handled := replayTransactionResponseWithOverride("tx-new", "new-hash", &record, nil, false)
\tif !handled || blocked == nil || !blocked.RecoveryRequired {
\t\tt.Fatalf("new mutation was not blocked: handled=%v response=%+v", handled, blocked)
\t}
\tallowed, handled := replayTransactionResponseWithOverride("boot-reconcile-new", "canonical-hash", &record, nil, true)
\tif handled || allowed != nil {
\t\tt.Fatalf("canonical reconcile was blocked: handled=%v response=%+v", handled, allowed)
\t}
}

func TestCorruptJournalBlocksMutationButAllowsCanonicalReconcile(t *testing.T) {
\tblocked, handled := replayTransactionResponseWithOverride("tx-new", "hash", nil, errors.New("corrupt journal"), false)
\tif !handled || blocked == nil || !blocked.RecoveryRequired {
\t\tt.Fatalf("corrupt journal did not block mutation: handled=%v response=%+v", handled, blocked)
\t}
\tallowed, handled := replayTransactionResponseWithOverride("boot-reconcile-new", "hash", nil, errors.New("corrupt journal"), true)
\tif handled || allowed != nil {
\t\tt.Fatalf("canonical reconcile could not replace corrupt journal: handled=%v response=%+v", handled, allowed)
\t}
}
''')
