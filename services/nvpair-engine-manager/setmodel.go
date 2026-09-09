// SPDX-FileCopyrightText: Copyright (c) 2026 Jarrett Simerson
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// SetModel persists the model a one-model-per-process engine serves and applies
// it: the running session is bounced onto the new model and the cached value is
// updated so a later start uses it.
//
// It is deliberately the same shape as SetPort rather than a model operation.
// An engine like MAX compiles and loads one model per process with no hot swap,
// so "load this model" really is "reconfigure and restart" — modelling it as
// launch configuration keeps one code path instead of a load that silently
// means something different per engine.
//
// A running, adopted engine is refused, inheriting SetPort's rule: a `max serve`
// that NVPAIR adopted rather than started cannot be relaunched onto a different
// model without killing a process it does not own.
func (e *Executor) SetModel(ctx context.Context, engine, model string) (EngineStatus, error) {
	model = strings.TrimSpace(model)
	if err := validModelRef(model); err != nil {
		return EngineStatus{}, err
	}
	st, err := e.state(engine)
	if err != nil {
		return EngineStatus{}, err
	}
	if !runtimeNeedsModel(st.plat.Runtime) {
		return EngineStatus{}, fmt.Errorf("engine %q does not select a model at launch; load it as a model operation instead", engine)
	}
	st.opMu.Lock()
	defer st.opMu.Unlock()

	st.mu.Lock()
	wasRunning := st.running
	adopted := st.adopted
	oldModel := st.plat.Runtime.Model
	st.mu.Unlock()

	if wasRunning && adopted && !canMoveAdoptedEngine(st.plat.Runtime) {
		return EngineStatus{}, fmt.Errorf("cannot change %s's model: it is running under external management (NVPAIR adopted it rather than starting it), so NVPAIR cannot restart it — stop it in its own app first, then choose a model", engine)
	}
	if oldModel == model && wasRunning {
		// Already serving it. Restarting would cost a multi-minute recompile
		// for no change.
		return e.snapshot(engine, st), nil
	}

	if wasRunning {
		if err := e.doStop(st, engine); err != nil {
			return EngineStatus{}, err
		}
	}

	if err := e.persistRuntimeField(engine, "model", model); err != nil {
		if !wasRunning {
			return EngineStatus{}, err
		}
		st.mu.Lock()
		st.plat.Runtime.Model = oldModel
		st.mu.Unlock()
		restartErr := e.doStart(ctx, st, engine, startOpts{})
		return EngineStatus{}, errors.Join(err, restartErr)
	}

	st.mu.Lock()
	st.plat.Runtime.Model = model
	st.mu.Unlock()

	if wasRunning {
		// doStart re-reads st.plat.Runtime and emits engine:state-changed.
		if err := e.doStart(ctx, st, engine, startOpts{}); err != nil {
			return EngineStatus{}, err
		}
	} else {
		e.emitState(engine)
	}
	return e.snapshot(engine, st), nil
}

// validModelRef rejects a reference that cannot be a model.
//
// The value becomes one argv element, never a shell string, so quoting is not
// the concern. What matters is that a control character or newline would be
// passed through to the engine and produce an unreadable failure far from here,
// and that an empty selection must not look like a successful choice.
func validModelRef(model string) error {
	if model == "" {
		return fmt.Errorf("model is required")
	}
	for _, r := range model {
		if unicode.IsControl(r) {
			return fmt.Errorf("model %q contains a control character", model)
		}
	}
	if strings.HasPrefix(model, "-") {
		return fmt.Errorf("model %q looks like a command-line flag", model)
	}
	return nil
}
