package main

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/vladimirperovic/minimalrouter/internal/apply"
	"github.com/vladimirperovic/minimalrouter/internal/runtimebudget"
)

func TestStartupBudgetCoversCanonicalReconcileAndSlowServiceStart(t *testing.T) {
	if runtimebudget.Reconcile != apply.ReconcileBudget {
		t.Fatal("startup contract drifted from canonical reconcile")
	}
	if runtimebudget.Startup <= apply.ReconcileBudget {
		t.Fatal("OpenRC cannot accommodate reconcile")
	}
	for _, role := range []string{"routerd", "router-applyd"} {
		for _, op := range []string{"start", "restart"} {
			if serviceTimeout([]string{role, op}) <= runtimebudget.Startup {
				t.Fatal("updater cuts short an allowed slow OpenRC start")
			}
		}
	}
	if serviceTimeout([]string{"routerd", "stop"}) != 30*time.Second {
		t.Fatal("stop became unbounded")
	}
	var out, errout bytes.Buffer
	if code := run([]string{"startup-budget"}, 1000, &out, &errout); code != 0 {
		t.Fatal(errout.String())
	}
	if out.String() != fmt.Sprintln(int(runtimebudget.Startup/time.Second)) {
		t.Fatal("OpenRC sees a different startup budget")
	}
}
