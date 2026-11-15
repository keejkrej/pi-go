// Ported from packages/durable/src/documents.ts (pi v1.0.0).

package durable

import (
	"fmt"

	"github.com/keejkrej/pi-go/chord"
	"github.com/keejkrej/pi-go/internal/js"
	"github.com/keejkrej/pi-go/internal/jsonx"
)

// DefineDoc defines a singleton document token after validating its persisted version semantics.
func DefineDoc(definition DocDefinition) (*DocToken, error) {
	def := docuNormalizeSingleton(definition)
	if err := docuValidateDefinition(def); err != nil {
		return nil, err
	}
	return &DocToken{Definition: def}, nil
}

// DefineDocFamily defines a keyed document family token after validating its persisted version semantics.
func DefineDocFamily(definition DocFamilyDefinition) (*DocFamilyToken, error) {
	def := docuNormalizeFamily(definition)
	if err := docuValidateDefinition(def); err != nil {
		return nil, err
	}
	return &DocFamilyToken{Definition: def}, nil
}

func docuMustDefineDoc(definition DocDefinition) *DocToken {
	token, err := DefineDoc(definition)
	if err != nil {
		panic(err)
	}
	return token
}

func docuMustDefineDocFamily(definition DocFamilyDefinition) *DocFamilyToken {
	token, err := DefineDocFamily(definition)
	if err != nil {
		panic(err)
	}
	return token
}

func docuNormalizeSingleton(d DocDefinition) *AnyDocDefinition {
	initial := d.Initial
	return &AnyDocDefinition{
		Kind:           d.Kind,
		Version:        d.Version,
		Scope:          d.Scope,
		History:        d.History,
		Fork:           d.Fork,
		Family:         false,
		Initial:        func(_ JsonValue) JsonObject { return initial() },
		Migrate:        d.Migrate,
		CheckpointWhen: d.CheckpointWhen,
	}
}

func docuNormalizeFamily(d DocFamilyDefinition) *AnyDocDefinition {
	return &AnyDocDefinition{
		Kind:           d.Kind,
		Version:        d.Version,
		Scope:          d.Scope,
		History:        d.History,
		Fork:           d.Fork,
		Family:         true,
		Initial:        d.Initial,
		Migrate:        d.Migrate,
		CheckpointWhen: d.CheckpointWhen,
	}
}

func docuValidateDefinition(definition *AnyDocDefinition) error {
	if definition == nil || definition.Version < 1 {
		kind := ""
		if definition != nil {
			kind = definition.Kind
		}
		return js.NewTypeError(fmt.Sprintf("Document %s version must be a positive integer", kind))
	}
	return nil
}

// ResolvedAddress is a logical address plus its string identity for map lookups.
type ResolvedAddress struct {
	Address      DocumentAddress
	Id           string
	NextArgument int
}

// ResolveAddress resolves an overloaded argument list and returns the index after the owner and family key.
func ResolveAddress(definition *AnyDocDefinition, args []any) (*ResolvedAddress, error) {
	if definition == nil {
		return nil, js.NewTypeError("Document definition is required")
	}
	index := 0
	var scope DocumentScope
	switch definition.Scope {
	case DocumentScopeSession:
		scope = DocumentScope{Kind: DocumentScopeSession}
	case DocumentScopeConversation:
		if index >= len(args) {
			return nil, js.NewTypeError(fmt.Sprintf("Document %s requires a %s ID", definition.Kind, definition.Scope))
		}
		id, err := docuOwnerId[ConversationId](args[index], definition)
		if err != nil {
			return nil, err
		}
		index++
		scope = DocumentScope{Kind: DocumentScopeConversation, ConversationId: id}
	case DocumentScopeTask:
		if index >= len(args) {
			return nil, js.NewTypeError(fmt.Sprintf("Document %s requires a %s ID", definition.Kind, definition.Scope))
		}
		id, err := docuOwnerId[TaskId](args[index], definition)
		if err != nil {
			return nil, err
		}
		index++
		scope = DocumentScope{Kind: DocumentScopeTask, TaskId: id}
	default:
		return nil, js.NewTypeError(fmt.Sprintf("Unknown document scope %q", definition.Scope))
	}

	var keyPtr *string
	if definition.Family {
		if index >= len(args) {
			return nil, js.NewTypeError(fmt.Sprintf("Document family %s requires a string key", definition.Kind))
		}
		key, ok := args[index].(string)
		if !ok {
			return nil, js.NewTypeError(fmt.Sprintf("Document family %s requires a string key", definition.Kind))
		}
		index++
		keyPtr = &key
	}
	address := DocumentAddress{
		Kind:  definition.Kind,
		Scope: scope,
		Key:   keyPtr,
	}
	return &ResolvedAddress{
		Address:      address,
		Id:           AddressId(address),
		NextArgument: index,
	}, nil
}

// docuCheckExactArgs verifies that no unexpected trailing arguments follow the resolved address and expected trailing arguments.
func docuCheckExactArgs(def *AnyDocDefinition, resolved *ResolvedAddress, args []any, trailing int, method string) error {
	expected := resolved.NextArgument + trailing
	if len(args) > expected {
		return js.NewTypeError(fmt.Sprintf("%s received unexpected extra argument for document kind %q", method, def.Kind))
	}
	return nil
}

func docuOwnerId[I ~int64](value any, definition *AnyDocDefinition) (I, error) {
	switch v := value.(type) {
	case ConversationId:
		if definition.Scope == DocumentScopeConversation {
			return I(v), nil
		}
	case TaskId:
		if definition.Scope == DocumentScopeTask {
			return I(v), nil
		}
	case int64:
		return IdFromNumber[I](v), nil
	case int:
		return IdFromNumber[I](int64(v)), nil
	case float64:
		if float64(int64(v)) == v {
			return IdFromNumber[I](int64(v)), nil
		}
	}
	return 0, js.NewTypeError(fmt.Sprintf("Document %s requires a %s ID", definition.Kind, definition.Scope))
}

// AddressId returns the stable string identity of one logical address.
func AddressId(address DocumentAddress) string {
	var owner any
	switch address.Scope.Kind {
	case DocumentScopeSession:
		owner = nil
	case DocumentScopeConversation:
		owner = float64(address.Scope.ConversationId)
	case DocumentScopeTask:
		owner = float64(address.Scope.TaskId)
	}
	var key any
	if address.Key != nil {
		key = *address.Key
	}
	return jsonx.MustStringify([]any{address.Kind, string(address.Scope.Kind), owner, key})
}

// NewDocumentCreate builds the storage create record for a new incarnation at an address.
func NewDocumentCreate(definition *AnyDocDefinition, address DocumentAddress, id DocumentId) DocumentCreate {
	rec := DocumentCreate{
		Id:    id,
		Kind:  address.Kind,
		Key:   docuCopyStringPtr(address.Key),
		Scope: address.Scope,
	}
	if address.Scope.Kind == DocumentScopeConversation {
		rec.History = definition.History
		rec.Fork = definition.Fork
	}
	return rec
}

func docuCopyStringPtr(s *string) *string {
	if s == nil {
		return nil
	}
	v := *s
	return &v
}

// CheckRecordScope rejects typed access whose token disagrees with the persisted scope, history, or fork semantics.
func CheckRecordScope(definition *AnyDocDefinition, record DocumentRecord) error {
	if record.Scope.Kind != definition.Scope ||
		(record.Scope.Kind == DocumentScopeConversation &&
			(record.History != definition.History || record.Fork != definition.Fork)) {
		return js.NewTypeError(fmt.Sprintf("Document %d (%s) does not match the supplied definition semantics", record.Id, record.Kind))
	}
	return nil
}

// CheckRecordVersion rejects typed access to a stored version the supplied definition cannot use.
func CheckRecordVersion(definition *AnyDocDefinition, record DocumentRecord, version int) error {
	if version > definition.Version {
		return fmt.Errorf("Document %d (%s) has newer version %d than %d", record.Id, record.Kind, version, definition.Version)
	}
	if version < definition.Version && definition.Migrate == nil {
		return fmt.Errorf("Document %d (%s) requires migration from version %d", record.Id, record.Kind, version)
	}
	return nil
}

// MaterializeDocument validates and materializes a detached stored value for typed access.
func MaterializeDocument(definition *AnyDocDefinition, stored StoredDocument) (JsonObject, error) {
	return MaterializeDocumentValue(definition, stored.Record, stored.Version, stored.Value)
}

// MaterializeDocumentValue validates and materializes one detached value before its first persisted incarnation.
func MaterializeDocumentValue(definition *AnyDocDefinition, record DocumentRecord, version int, value JsonObject) (JsonObject, error) {
	if err := CheckRecordScope(definition, record); err != nil {
		return nil, err
	}
	if err := CheckRecordVersion(definition, record, version); err != nil {
		return nil, err
	}
	if version == definition.Version {
		return value, nil
	}
	migrated := definition.Migrate(value, version)
	copied, err := chord.CopyJson(migrated, nil)
	if err != nil {
		return nil, err
	}
	obj, ok := copied.(JsonObject)
	if !ok || obj == nil {
		return nil, js.NewTypeError(fmt.Sprintf("Document %d (%s) migration must return a JSON object", record.Id, record.Kind))
	}
	return obj, nil
}
