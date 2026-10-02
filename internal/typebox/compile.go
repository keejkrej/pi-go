package typebox

import (
	"fmt"
	"reflect"

	"github.com/keejkrej/pi-go/internal/jsonx"
)

// Validator is a compiled schema. Compilation checks patterns; checks run
// on the TypeBox interpreter.
type Validator struct {
	schema *Schema
}

// Compile prepares s for repeated checks. A nil schema or an invalid
// JavaScript pattern is an error.
func Compile(s *Schema) (*Validator, error) {
	if s == nil {
		return nil, fmt.Errorf("typebox: nil schema")
	}
	if err := validatePatterns(s, map[uintptr]struct{}{}); err != nil {
		return nil, err
	}
	return &Validator{schema: s}, nil
}

// Check reports whether x matches the compiled schema.
func (v *Validator) Check(x any) bool {
	if v == nil {
		return false
	}
	return Check(v.schema, x)
}

// Errors returns localized errors. A matching value yields an empty slice.
func (v *Validator) Errors(x any) []ValidationError {
	if v == nil {
		return []ValidationError{}
	}
	if v.Check(x) {
		return []ValidationError{}
	}
	return Errors(v.schema, x)
}

func validatePatterns(n any, seen map[uintptr]struct{}) error {
	if _, ok := schemaBool(n); ok {
		return nil
	}
	o, ok := schemaObject(n)
	if !ok {
		return nil
	}
	ptr := reflect.ValueOf(o).Pointer()
	if _, ok := seen[ptr]; ok {
		return nil
	}
	seen[ptr] = struct{}{}
	if pat, ok := o.GetString("pattern"); ok {
		if _, err := compileRE(pat, "u"); err != nil {
			return fmt.Errorf("typebox: invalid pattern %q: %w", pat, err)
		}
	}
	if pp, ok := o.GetObject("patternProperties"); ok {
		for _, k := range pp.Keys() {
			if _, err := compileRE(k, "u"); err != nil {
				return fmt.Errorf("typebox: invalid pattern %q: %w", k, err)
			}
		}
	}
	for _, k := range o.Keys() {
		if k == "const" || k == "enum" {
			continue
		}
		v, _ := o.Get(k)
		if err := walkPatterns(v, seen); err != nil {
			return err
		}
	}
	return nil
}

func walkPatterns(v any, seen map[uintptr]struct{}) error {
	switch x := v.(type) {
	case *Schema, *jsonx.Object:
		return validatePatterns(x, seen)
	case []any:
		for _, el := range x {
			if err := walkPatterns(el, seen); err != nil {
				return err
			}
		}
	}
	return nil
}
