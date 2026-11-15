// Ported from packages/durable/src/tasks.ts (pi v1.0.0).

package durable

import (
	"fmt"
	"reflect"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefineTask defines an executable task. Register it in the registry so a Harness can run tasks of its kind.
func DefineTask[I, S, R, H any](definition TaskDefinition[I, S, R, H]) *Task[I, S, R, H] {
	return &Task[I, S, R, H]{Definition: definition}
}

// Task is a typed executable task definition implementing AnyTask.
type Task[I, S, R, H any] struct {
	Definition TaskDefinition[I, S, R, H]
}

// TaskKind returns the task definition name.
func (t *Task[I, S, R, H]) TaskKind() string { return t.Definition.Name }

// TaskVersion returns the task definition version.
func (t *Task[I, S, R, H]) TaskVersion() int { return t.Definition.Version }

func (t *Task[I, S, R, H]) erasedDefinition() *AnyTaskDefinition {
	def := t.Definition
	phases := make(map[string]AnyPhaseHandler, len(def.Phases))
	for name, handler := range def.Phases {
		h := handler
		phases[name] = func(ctx chord.Context, raw *RunningTaskRecord, runtime AnyTaskRuntime) error {
			running, err := tskDecodeRunning[I, S](raw)
			if err != nil {
				return err
			}
			wrappedRuntime := tskWrapRuntime[I, S, R, H](runtime)
			return h(ctx, running, wrappedRuntime)
		}
	}
	var migrate func(input JsonValue, checkpoint JsonValue, fromVersion int) (JsonValue, JsonValue, error)
	if def.Migrate != nil {
		m := def.Migrate
		migrate = func(rawIn JsonValue, rawCp JsonValue, fromVersion int) (JsonValue, JsonValue, error) {
			in, cp, err := m(rawIn, rawCp, fromVersion)
			if err != nil {
				return nil, nil, err
			}
			inJson, err := tskEncodeJsonValue(in)
			if err != nil {
				return nil, nil, err
			}
			cpJson, err := tskEncodeJsonValue(cp)
			if err != nil {
				return nil, nil, err
			}
			return inJson, cpJson, nil
		}
	}
	var abortHandler AnyPhaseHandler
	if def.Abort != nil {
		a := def.Abort
		abortHandler = func(ctx chord.Context, raw *RunningTaskRecord, runtime AnyTaskRuntime) error {
			running, err := tskDecodeRunning[I, S](raw)
			if err != nil {
				return err
			}
			wrappedRuntime := tskWrapRuntime[I, S, R, H](runtime)
			return a(ctx, running, wrappedRuntime)
		}
	}
	return &AnyTaskDefinition{
		Name:    def.Name,
		Version: def.Version,
		Initial: func(rawInput any) (JsonValue, JsonValue, error) {
			var in I
			inJson, err := tskEncodeJsonValue(rawInput)
			if err != nil {
				return nil, nil, err
			}
			if typed, ok := rawInput.(I); ok {
				in = typed
			} else {
				if err := jsonx.Decode(inJson, &in); err != nil {
					return nil, nil, err
				}
			}
			cp := def.Initial(in)
			cpJson, err := tskEncodeJsonValue(cp)
			if err != nil {
				return nil, nil, err
			}
			return inJson, cpJson, nil
		},
		Phases:  phases,
		Abort:   abortHandler,
		Migrate: migrate,
		Hooks:   def.Hooks,
	}
}

func tskDecodeRunning[I, S any](raw *RunningTaskRecord) (RunningTask[I, S], error) {
	var in I
	if raw.Input != nil {
		if typed, ok := raw.Input.(I); ok {
			in = typed
		} else if err := jsonx.Decode(raw.Input, &in); err != nil {
			return RunningTask[I, S]{}, fmt.Errorf("decode task input: %w", err)
		}
	}
	var cp S
	if raw.State.Checkpoint != nil {
		if typed, ok := raw.State.Checkpoint.(S); ok {
			cp = typed
		} else if err := jsonx.Decode(raw.State.Checkpoint, &cp); err != nil {
			return RunningTask[I, S]{}, fmt.Errorf("decode task checkpoint: %w", err)
		}
	}
	return RunningTask[I, S]{
		Id:             raw.Id,
		ConversationId: raw.ConversationId,
		Kind:           raw.Kind,
		Version:        raw.Version,
		Input:          in,
		Owner:          raw.Owner,
		Background:     raw.Background,
		AbortRequested: raw.AbortRequested,
		Checkpoint:     cp,
		Memos:          raw.Memos,
	}, nil
}

func tskEncodeJsonValue(v any) (JsonValue, error) {
	if v == nil {
		return nil, nil
	}
	encoded, err := jsonx.Encode(v)
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func tskWrapRuntime[I, S, R, H any](runtime AnyTaskRuntime) *TaskRuntime[I, S, R, H] {
	return &TaskRuntime[I, S, R, H]{
		base: runtime,
		hooks: HookRunner[H]{
			raw: runtime.HooksAny(),
		},
	}
}

func tskConvertHooks[H any](raw any) (H, bool) {
	if raw == nil {
		var zero H
		return zero, false
	}
	if h, ok := raw.(H); ok {
		return h, true
	}
	rv := reflect.ValueOf(raw)
	var zero H
	targetType := reflect.TypeOf(&zero).Elem()
	if rv.Kind() == reflect.Pointer && !rv.IsNil() && rv.Elem().Type().AssignableTo(targetType) {
		return rv.Elem().Interface().(H), true
	}
	return zero, false
}
