package apply

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// WithQoSBypassed runs fn while the active runtime has QoS shaping removed,
// then restores the canonical configuration before returning. The canonical
// in-memory/SQLite configuration is never modified.
//
// The temporary apply uses DeferLastGood so router-applyd never advances its
// durable last-good state to the measurement-only runtime. A final RECONCILE
// restores the canonical runtime and clears the helper's provisional state.
// operationMu is held for the complete window so no configuration transaction
// can race the temporary runtime.
func (e *Engine) WithQoSBypassed(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("temporary QoS callback is required")
	}

	e.operationMu.Lock()
	defer e.operationMu.Unlock()

	e.mu.RLock()
	if e.recoveryRequired {
		reason := e.recoveryReason
		e.mu.RUnlock()
		if reason == "" {
			return fmt.Errorf("temporary QoS bypass is blocked while recovery is required")
		}
		return fmt.Errorf("temporary QoS bypass is blocked while recovery is required: %s", reason)
	}
	if e.pending != nil {
		e.mu.RUnlock()
		return fmt.Errorf("temporary QoS bypass is blocked while a configuration change awaits confirmation")
	}
	canonical := e.currentConfig.DeepCopy()
	e.mu.RUnlock()

	if !canonical.QoS.Enabled {
		return fn(ctx)
	}

	bypass := canonical.DeepCopy()
	bypass.QoS.Enabled = false
	bypassID := fmt.Sprintf("speedtest-qos-bypass-%d", time.Now().UnixNano())
	bypassReq, err := buildApplyRequest(bypassID, bypass)
	if err != nil {
		return fmt.Errorf("build temporary QoS bypass: %w", err)
	}
	bypassReq.RequireConfirmation = false
	bypassReq.DeferLastGood = true

	if err := e.applyTemporaryRuntime(bypassReq); err != nil {
		return fmt.Errorf("activate temporary QoS bypass: %w", err)
	}

	measurementErr := fn(ctx)

	// Restoration deliberately ignores request cancellation. Closing the tab may
	// cancel the measurement, but it must never leave QoS disabled afterwards.
	restoreID := fmt.Sprintf("speedtest-qos-restore-%d", time.Now().UnixNano())
	restoreReq, buildErr := buildApplyRequest(restoreID, canonical)
	if buildErr == nil {
		restoreReq.Op = OpReconcile
		restoreReq.RequireConfirmation = false
		restoreReq.DeferLastGood = false
	}
	var restoreErr error
	if buildErr != nil {
		restoreErr = fmt.Errorf("build canonical QoS restore: %w", buildErr)
	} else {
		restoreErr = e.applyTemporaryRuntime(restoreReq)
		if restoreErr != nil {
			restoreErr = fmt.Errorf("restore canonical QoS runtime: %w", restoreErr)
		}
	}

	if restoreErr != nil {
		e.mu.Lock()
		e.requireRecovery("temporary QoS bypass could not restore the canonical runtime: " + restoreErr.Error())
		e.mu.Unlock()
	}

	return errors.Join(measurementErr, restoreErr)
}

func (e *Engine) applyTemporaryRuntime(req ApplyRequest) error {
	applyCtx, cancel := context.WithTimeout(context.Background(), privilegedApplyTimeout)
	defer cancel()

	e.mu.Lock()
	e.applying = true
	e.mu.Unlock()
	resp, err := e.applyPrivileged(applyCtx, req)
	e.mu.Lock()
	e.applying = false
	e.mu.Unlock()
	if err != nil {
		return err
	}
	if resp == nil {
		return fmt.Errorf("router-applyd returned an empty response")
	}
	if !resp.Success || !resp.Verified {
		if resp.Error != "" {
			return fmt.Errorf("router-applyd rejected temporary runtime: %s", resp.Error)
		}
		return fmt.Errorf("router-applyd did not verify temporary runtime")
	}
	return nil
}
