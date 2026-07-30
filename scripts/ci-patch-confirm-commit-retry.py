from pathlib import Path


def replace_once(text: str, old: str, new: str, name: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{name}: expected exactly one occurrence, found {count}")
    return text.replace(old, new, 1)


state_path = Path("internal/apply/statemachine.go")
state = state_path.read_text()
state = replace_once(
    state,
    '''type pendingChange struct {
\ttx                 *Transaction
\tprevious           config.SystemConfig
\ttimer              *time.Timer
\trollbackAttempts   int
\tcanonicalCommitted bool
}''',
    '''type pendingChange struct {
\ttx                 *Transaction
\tprevious           config.SystemConfig
\ttimer              *time.Timer
\trollbackAttempts   int
\tcommitAttempts     int
\tcanonicalCommitted bool
}''',
    "pending commit attempt counter",
)
state = replace_once(
    state,
    '''\tcommitReq := ApplyRequest{ID: txID + "-commit-confirmed", Op: OpCommitConfirmed, Revision: pending.tx.Config.Revision, Config: pending.tx.Config}
\tcommitCtx, commitCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)''',
    '''\tpending.commitAttempts++
\tcommitID := fmt.Sprintf("%s-commit-confirmed-%d", txID, pending.commitAttempts)
\tcommitReq := ApplyRequest{ID: commitID, Op: OpCommitConfirmed, Revision: pending.tx.Config.Revision, Config: pending.tx.Config}
\tcommitCtx, commitCancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)''',
    "fresh confirmed commit ID",
)
state_path.write_text(state)

test_path = Path("internal/apply/two_phase_confirmation_test.go")
test = test_path.read_text()
test = replace_once(
    test,
    '''\tfor i, op := range want {
\t\tif client.requests[i].Op != op {
\t\t\tt.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
\t\t}
\t}
}''',
    '''\tfor i, op := range want {
\t\tif client.requests[i].Op != op {
\t\t\tt.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
\t\t}
\t}
\tif client.requests[2].ID != tx.ID+"-commit-confirmed-1" {
\t\tt.Fatalf("first confirmed commit ID=%q", client.requests[2].ID)
\t}
}''',
    "ordered confirmation commit ID assertion",
)
test = replace_once(
    test,
    '''\tfor i, op := range want {
\t\tif client.requests[i].Op != op {
\t\t\tt.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
\t\t}
\t}
}''',
    '''\tfor i, op := range want {
\t\tif client.requests[i].Op != op {
\t\t\tt.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, op)
\t\t}
\t}
\tif client.requests[2].ID == client.requests[3].ID {
\t\tt.Fatalf("explicit confirmed-commit retry reused cached transaction ID %q", client.requests[2].ID)
\t}
\tif client.requests[2].ID != tx.ID+"-commit-confirmed-1" || client.requests[3].ID != tx.ID+"-commit-confirmed-2" {
\t\tt.Fatalf("unexpected confirmed-commit retry IDs: %q, %q", client.requests[2].ID, client.requests[3].ID)
\t}
}''',
    "retry confirmation commit ID assertions",
)
test_path.write_text(test)
