from pathlib import Path


def replace_once(text: str, old: str, new: str, name: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{name}: expected exactly one occurrence, found {count}")
    return text.replace(old, new, 1)


failure_path = Path("internal/apply/failure_scenarios_test.go")
failure = failure_path.read_text()
failure = replace_once(
    failure,
    '''\tclient := &scenarioApplyClient{steps: []scenarioApplyStep{
\t\tsuccessfulScenarioStep(),
\t\t{err: errors.New("confirmation response lost")},
\t\tsuccessfulScenarioStep(),
\t}}''',
    '''\tclient := &scenarioApplyClient{steps: []scenarioApplyStep{
\t\tsuccessfulScenarioStep(),
\t\t{err: errors.New("confirmation response lost")},
\t\tsuccessfulScenarioStep(),
\t\tsuccessfulScenarioStep(),
\t}}''',
    "confirmation response-loss fixture",
)
failure = replace_once(
    failure,
    '''\tif len(client.requests) != 3 {
\t\tt.Fatalf("expected apply plus two confirmation attempts, got %d requests", len(client.requests))
\t}
\tif client.requests[1].ID != client.requests[2].ID {
\t\tt.Fatalf("confirmation retry changed transaction ID: %q != %q", client.requests[1].ID, client.requests[2].ID)
\t}''',
    '''\tif len(client.requests) != 4 {
\t\tt.Fatalf("expected apply, two runtime-confirm attempts, and canonical commit, got %d requests", len(client.requests))
\t}
\tif client.requests[1].Op != OpConfirm || client.requests[2].Op != OpConfirm {
\t\tt.Fatalf("runtime confirmation retry used unexpected operations: %s, %s", client.requests[1].Op, client.requests[2].Op)
\t}
\tif client.requests[1].ID != client.requests[2].ID {
\t\tt.Fatalf("confirmation retry changed transaction ID: %q != %q", client.requests[1].ID, client.requests[2].ID)
\t}
\tif client.requests[3].Op != OpCommitConfirmed {
\t\tt.Fatalf("final helper operation=%s, want %s", client.requests[3].Op, OpCommitConfirmed)
\t}
\tfor i := 1; i < len(client.requests); i++ {
\t\tif client.requests[i].Revision != client.requests[0].Revision {
\t\t\tt.Fatalf("request %d changed confirmed revision", i)
\t\t}
\t}''',
    "confirmation response-loss assertions",
)
failure_path.write_text(failure)

state_path = Path("internal/apply/statemachine_test.go")
state = state_path.read_text()
state = replace_once(
    state,
    '''\tif len(client.requests) != 2 || client.requests[1].Config.Revision != client.requests[0].Config.Revision {
\t\tt.Fatalf("confirmation did not use the exact applied revision")
\t}''',
    '''\tif len(client.requests) != 3 {
\t\tt.Fatalf("expected apply, runtime confirmation, and canonical helper commit; got %d requests", len(client.requests))
\t}
\twantOps := []OperationType{OpApplyAll, OpConfirm, OpCommitConfirmed}
\tfor i, wantOp := range wantOps {
\t\tif client.requests[i].Op != wantOp {
\t\t\tt.Fatalf("request %d op=%s, want %s", i, client.requests[i].Op, wantOp)
\t\t}
\t\tif client.requests[i].Config.Revision != client.requests[0].Config.Revision {
\t\t\tt.Fatalf("request %d did not use the exact applied revision", i)
\t\t}
\t}''',
    "transaction lifecycle confirmation assertions",
)
state_path.write_text(state)
