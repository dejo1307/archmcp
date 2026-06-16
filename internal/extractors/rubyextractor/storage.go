package rubyextractor

import (
	"strings"
)

// ActiveRecord base-class detection.
var (
	// Base classes that indicate an ActiveRecord model.
	arBaseClasses = []string{
		"ApplicationRecord",
		"ActiveRecord::Base",
	}
	// Suffix convention for abstract base models (e.g. ItemsModel, ShippingModel).
	arModelSuffix = "Model"
)

// isARBaseClass returns true if the superclass indicates an ActiveRecord model.
func isARBaseClass(superclass string) bool {
	if superclass == "" {
		return false
	}
	for _, base := range arBaseClasses {
		if superclass == base {
			return true
		}
	}
	// Convention: classes ending in "Model" are often abstract AR bases (e.g. ItemsModel).
	if strings.HasSuffix(superclass, arModelSuffix) {
		return true
	}
	return false
}

// inferTableName derives the conventional Rails table name from a class name.
// e.g. "Item" -> "items", "UserAddress" -> "user_addresses", "Api::V2::Item" -> "items"
func inferTableName(className string) string {
	// Take the last segment if it's a qualified name.
	parts := strings.Split(className, "::")
	name := parts[len(parts)-1]

	// Convert CamelCase to snake_case.
	snake := camelToSnake(name)

	// Simple pluralization.
	return pluralize(snake)
}

// camelToSnake converts CamelCase to snake_case.
func camelToSnake(s string) string {
	var result []byte
	for i, ch := range s {
		if ch >= 'A' && ch <= 'Z' {
			if i > 0 {
				result = append(result, '_')
			}
			result = append(result, byte(ch-'A'+'a'))
		} else {
			result = append(result, byte(ch))
		}
	}
	return string(result)
}

// snakeToCamel converts snake_case to CamelCase.
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// pluralize applies simple English pluralization rules.
func pluralize(s string) string {
	if s == "" {
		return s
	}
	if strings.HasSuffix(s, "ss") || strings.HasSuffix(s, "sh") ||
		strings.HasSuffix(s, "ch") || strings.HasSuffix(s, "x") ||
		strings.HasSuffix(s, "z") {
		return s + "es"
	}
	if strings.HasSuffix(s, "y") && len(s) > 1 {
		preceding := s[len(s)-2]
		if preceding != 'a' && preceding != 'e' && preceding != 'i' && preceding != 'o' && preceding != 'u' {
			return s[:len(s)-1] + "ies"
		}
	}
	if strings.HasSuffix(s, "s") {
		return s
	}
	return s + "s"
}

// singularize applies simple English singularization (inverse of pluralize).
func singularize(s string) string {
	if strings.HasSuffix(s, "ies") && len(s) > 3 {
		return s[:len(s)-3] + "y"
	}
	if strings.HasSuffix(s, "sses") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "shes") || strings.HasSuffix(s, "ches") ||
		strings.HasSuffix(s, "xes") || strings.HasSuffix(s, "zes") {
		return s[:len(s)-2]
	}
	if strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss") {
		return s[:len(s)-1]
	}
	return s
}
