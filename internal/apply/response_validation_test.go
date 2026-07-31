package apply

import "testing"

func TestApplyResponseValidation(t *testing.T) {
	valid := []ApplyResponse{
		{Success: true, Verified: true},
		{Success: false},
		{Success: false, RolledBack: true},
		{Success: false, RecoveryRequired: true},
	}
	for i, response := range valid {
		if err := response.Validate(); err != nil {
			t.Fatalf("valid response %d was rejected: %v", i, err)
		}
	}

	invalid := []ApplyResponse{
		{Success: true, Verified: false},
		{Success: true, Verified: true, RolledBack: true},
		{Success: true, Verified: true, RecoveryRequired: true},
		{Success: false, Verified: true},
		{Success: false, RolledBack: true, RecoveryRequired: true},
	}
	for i, response := range invalid {
		if err := response.Validate(); err == nil {
			t.Fatalf("invalid response %d was accepted: %+v", i, response)
		}
	}
}
