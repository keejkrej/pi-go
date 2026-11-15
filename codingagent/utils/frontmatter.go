// Ported from packages/coding-agent/src/utils/frontmatter.ts (pi v1.5.2).

package utils

import (
	"reflect"
	"strings"

	"github.com/keejkrej/pi-go/internal/js"
	"go.yaml.in/yaml/v3"
)

// ParsedFrontmatter holds parsed YAML frontmatter data and the remaining markdown body.
type ParsedFrontmatter[T any] struct {
	Frontmatter T
	Body        string
}

func fronNormalizeNewlines(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return value
}

func fronExtractFrontmatter(content string) (yamlString string, hasYaml bool, body string) {
	normalized := fronNormalizeNewlines(StripBom(content))

	if !strings.HasPrefix(normalized, "---") {
		return "", false, normalized
	}

	// Match JS normalized.indexOf("\n---", 3) using UTF-8 byte search since "\n---" is ASCII
	// and index 3 is after "---" (3 ASCII bytes).
	idx := strings.Index(normalized[3:], "\n---")
	if idx == -1 {
		return "", false, normalized
	}
	endIndex := 3 + idx
	if endIndex+4 > len(normalized) {
		endIndex = len(normalized) - 4
	}

	var yamlPart string
	if 4 <= endIndex {
		yamlPart = normalized[4:endIndex]
	}
	return yamlPart, yamlPart != "", js.Trim(normalized[endIndex+4:])
}

func fronInitZero[T any]() T {
	var zero T
	rv := reflect.ValueOf(&zero).Elem()
	if rv.Kind() == reflect.Map && rv.IsNil() {
		rv.Set(reflect.MakeMap(rv.Type()))
	}
	return zero
}

// ParseFrontmatter extracts and parses YAML frontmatter from markdown content.
func ParseFrontmatter[T any](content string) (ParsedFrontmatter[T], error) {
	yamlString, hasYaml, body := fronExtractFrontmatter(content)
	if !hasYaml {
		return ParsedFrontmatter[T]{
			Frontmatter: fronInitZero[T](),
			Body:        body,
		}, nil
	}

	parsed := fronInitZero[T]()
	if err := yaml.Unmarshal([]byte(yamlString), &parsed); err != nil {
		return ParsedFrontmatter[T]{}, err
	}
	rv := reflect.ValueOf(&parsed).Elem()
	if rv.Kind() == reflect.Map && rv.IsNil() {
		rv.Set(reflect.MakeMap(rv.Type()))
	}
	return ParsedFrontmatter[T]{
		Frontmatter: parsed,
		Body:        body,
	}, nil
}

// StripFrontmatter removes YAML frontmatter from content and returns the trimmed body.
func StripFrontmatter(content string) (string, error) {
	res, err := ParseFrontmatter[map[string]any](content)
	if err != nil {
		return "", err
	}
	return res.Body, nil
}
