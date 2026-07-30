from pathlib import Path


def replace_once(text: str, old: str, new: str, name: str) -> str:
    count = text.count(old)
    if count != 1:
        raise SystemExit(f"{name}: expected exactly one occurrence, found {count}")
    return text.replace(old, new, 1)


main_path = Path("cmd/router-applyd/main.go")
main = main_path.read_text()
if 'StartedAt   time.Time           `json:"started_at"`' not in main:
    main = replace_once(
        main,
        '''type transactionRecord struct {
\tID          string              `json:"id"`
\tConfigHash  string              `json:"config_hash"`
\tResponse    apply.ApplyResponse `json:"response"`
\tCompletedAt time.Time           `json:"completed_at"`
}''',
        '''type transactionRecord struct {
\tID          string              `json:"id"`
\tConfigHash  string              `json:"config_hash"`
\tResponse    apply.ApplyResponse `json:"response,omitempty"`
\tStartedAt   time.Time           `json:"started_at"`
\tCompletedAt time.Time           `json:"completed_at,omitempty"`
}''',
        "transaction record",
    )
    main = replace_once(
        main,
        '''\tlog.Printf("apply transaction %q revision %d", req.ID, req.Revision)
\tvar resp apply.ApplyResponse
\tif req.Op == apply.OpConfirm {
\t\tresp = confirmApply(req)
\t} else {
\t\tresp = applyAll(req)
\t}
\trecord := transactionRecord{
\t\tID: req.ID, ConfigHash: configHash, Response: resp, CompletedAt: time.Now(),
\t}
\trecord, resp = persistTransactionOutcome(record, saveLastTransaction)
\tlastTransactionMemory = &record
\twriteResponse(conn, resp)''',
        '''\tintent := transactionRecord{
\t\tID: req.ID, ConfigHash: configHash, StartedAt: time.Now(),
\t}
\tif err := saveLastTransaction(intent); err != nil {
\t\twriteResponse(conn, failure(req.ID, "privileged transaction intent could not be persisted", false))
\t\treturn
\t}
\tlastTransactionMemory = &intent

\tlog.Printf("apply transaction %q revision %d", req.ID, req.Revision)
\tvar resp apply.ApplyResponse
\tif req.Op == apply.OpConfirm {
\t\tresp = confirmApply(req)
\t} else {
\t\tresp = applyAll(req)
\t}
\trecord := transactionRecord{
\t\tID: req.ID, ConfigHash: configHash, Response: resp,
\t\tStartedAt: intent.StartedAt, CompletedAt: time.Now(),
\t}
\trecord, resp = persistTransactionOutcome(record, saveLastTransaction)
\tlastTransactionMemory = &record
\twriteResponse(conn, resp)''',
        "operation intent",
    )
    main = replace_once(
        main,
        '''func validateTransactionRecord(record transactionRecord) error {
\tif !transactionIDPattern.MatchString(record.ID) {
\t\treturn errors.New("transaction record has an invalid ID")
\t}
\tdigest, err := hex.DecodeString(record.ConfigHash)
\tif err != nil || len(digest) != sha256.Size {
\t\treturn errors.New("transaction record has an invalid configuration fingerprint")
\t}
\tif record.CompletedAt.IsZero() {
\t\treturn errors.New("transaction record has no completion time")
\t}
\tif record.Response.ID != record.ID {
\t\treturn errors.New("transaction record response ID does not match")
\t}
\tif err := record.Response.Validate(); err != nil {
\t\treturn fmt.Errorf("transaction record response is invalid: %w", err)
\t}
\treturn nil
}''',
        '''func validateTransactionRecord(record transactionRecord) error {
\tif !transactionIDPattern.MatchString(record.ID) {
\t\treturn errors.New("transaction record has an invalid ID")
\t}
\tdigest, err := hex.DecodeString(record.ConfigHash)
\tif err != nil || len(digest) != sha256.Size {
\t\treturn errors.New("transaction record has an invalid configuration fingerprint")
\t}
\tif record.StartedAt.IsZero() {
\t\treturn errors.New("transaction record has no start time")
\t}
\tif record.CompletedAt.IsZero() {
\t\tif record.Response.ID != "" || record.Response.Success || record.Response.Verified ||
\t\t\trecord.Response.RolledBack || record.Response.RecoveryRequired || record.Response.Error != "" {
\t\t\treturn errors.New("incomplete transaction record contains a final response")
\t\t}
\t\treturn nil
\t}
\tif record.CompletedAt.Before(record.StartedAt) {
\t\treturn errors.New("transaction record completion precedes start")
\t}
\tif record.Response.ID != record.ID {
\t\treturn errors.New("transaction record response ID does not match")
\t}
\tif err := record.Response.Validate(); err != nil {
\t\treturn fmt.Errorf("transaction record response is invalid: %w", err)
\t}
\treturn nil
}''',
        "transaction validator",
    )
main_path.write_text(main)

outcome_path = Path("cmd/router-applyd/outcome.go")
outcome = outcome_path.read_text()
if "privileged transaction outcome is incomplete" not in outcome:
    outcome = replace_once(
        outcome,
        '''\tif previous.ID != id {
\t\treturn nil, false
\t}
\tif previous.ConfigHash != configHash {
\t\tresponse := failure(id, "transaction ID was already used for different content", false)
\t\treturn &response, true
\t}
\tresponse := previous.Response
\treturn &response, true''',
        '''\tif previous.ID != id {
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
\treturn &response, true''',
        "incomplete intent replay",
    )
outcome_path.write_text(outcome)

validation_path = Path("cmd/router-applyd/persistence_validation_test.go")
validation = validation_path.read_text()
if "StartedAt: time.Now().Add(-time.Second)" not in validation:
    validation = replace_once(
        validation,
        '''\t\tResponse: apply.ApplyResponse{
\t\t\tID: "tx-valid-record", Success: true, Verified: true,
\t\t},
\t\tCompletedAt: time.Now(),''',
        '''\t\tResponse: apply.ApplyResponse{
\t\t\tID: "tx-valid-record", Success: true, Verified: true,
\t\t},
\t\tStartedAt: time.Now().Add(-time.Second), CompletedAt: time.Now(),''',
        "valid record fixture",
    )
    validation = replace_once(
        validation,
        '''\t\t{name: "missing completion", mutate: func(r *transactionRecord) { r.CompletedAt = time.Time{} }},''',
        '''\t\t{name: "missing start", mutate: func(r *transactionRecord) { r.StartedAt = time.Time{} }},
\t\t{name: "completion before start", mutate: func(r *transactionRecord) { r.CompletedAt = r.StartedAt.Add(-time.Second) }},''',
        "record time tests",
    )
if "TestValidateTransactionRecordAcceptsDurableIncompleteIntent" not in validation:
    validation += '''
func TestValidateTransactionRecordAcceptsDurableIncompleteIntent(t *testing.T) {
\trecord := validTransactionRecordForTest()
\trecord.Response = apply.ApplyResponse{}
\trecord.CompletedAt = time.Time{}
\tif err := validateTransactionRecord(record); err != nil {
\t\tt.Fatalf("durable incomplete intent rejected: %v", err)
\t}
\trecord.Response.Error = "must not exist before completion"
\tif err := validateTransactionRecord(record); err == nil {
\t\tt.Fatal("incomplete intent with final response was accepted")
\t}
}
'''
validation_path.write_text(validation)

persistence_path = Path("cmd/router-applyd/persistence_outcome_test.go")
persistence = persistence_path.read_text()
if "StartedAt: time.Now().Add(-time.Second)" not in persistence:
    persistence = replace_once(
        persistence,
        '''\tstored := &transactionRecord{
\t\tID: "tx-existing", ConfigHash: "same",
\t\tResponse: apply.ApplyResponse{ID: "tx-existing", Success: true, Verified: true},
\t}''',
        '''\tstored := &transactionRecord{
\t\tID: "tx-existing", ConfigHash: "same",
\t\tResponse: apply.ApplyResponse{ID: "tx-existing", Success: true, Verified: true},
\t\tStartedAt: time.Now().Add(-time.Second), CompletedAt: time.Now(),
\t}''',
        "persisted replay fixture",
    )
if "TestReplayIncompleteIntentRequiresRecoveryWithoutReapply" not in persistence:
    persistence = replace_once(persistence, '\t"testing"', '\t"testing"\n\t"time"', "time import")
    persistence += '''
func TestReplayIncompleteIntentRequiresRecoveryWithoutReapply(t *testing.T) {
\trecord := validTransactionRecordForTest()
\trecord.Response = apply.ApplyResponse{}
\trecord.CompletedAt = time.Time{}
\tresponse, handled := replayTransactionResponse(record.ID, record.ConfigHash, &record, nil)
\tif !handled || response == nil || !response.RecoveryRequired || response.RolledBack || response.Success {
\t\tt.Fatalf("incomplete intent replay handled=%v response=%+v", handled, response)
\t}
}
'''
persistence_path.write_text(persistence)
