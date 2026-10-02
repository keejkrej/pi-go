package jsdiff

import (
	"strconv"
	"strings"
)

// DMPChange is one entry of ConvertChangesToDMP: the diff-match-patch tuple
// [Operation, Value], where Operation is 1 (insert), -1 (delete) or 0 (equal). It
// marshals to JSON as that two-element array.
type DMPChange struct {
	Operation int
	Value     string
}

// MarshalJSON writes the tuple [operation, value].
func (c DMPChange) MarshalJSON() ([]byte, error) {
	return []byte("[" + strconv.Itoa(c.Operation) + "," + jsonString(c.Value) + "]"), nil
}

// ConvertChangesToDMP converts a list of change objects to the format returned by
// Google's diff-match-patch library.
func ConvertChangesToDMP(changes []Change) []DMPChange {
	ret := make([]DMPChange, 0, len(changes))
	for _, change := range changes {
		operation := 0
		if change.Added {
			operation = 1
		} else if change.Removed {
			operation = -1
		}
		ret = append(ret, DMPChange{Operation: operation, Value: change.Value})
	}
	return ret
}

// ConvertChangesToXML converts a list of change objects to a serialized XML format:
// added values are wrapped in <ins>, removed values in <del>, and &, <, > and " are
// escaped.
func ConvertChangesToXML(changes []Change) string {
	var ret strings.Builder
	for _, change := range changes {
		if change.Added {
			ret.WriteString("<ins>")
		} else if change.Removed {
			ret.WriteString("<del>")
		}
		ret.WriteString(escapeHTML(change.Value))
		if change.Added {
			ret.WriteString("</ins>")
		} else if change.Removed {
			ret.WriteString("</del>")
		}
	}
	return ret.String()
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")

func escapeHTML(s string) string {
	return htmlEscaper.Replace(s)
}
