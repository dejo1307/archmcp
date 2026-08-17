package asyncapiextractor

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
)

// messageInfo is deliberately lightweight. Enola resolves enough of the message
// and payload chain to identify the schema and its formats, but does not emit
// field-level facts until a compatibility or impact consumer can use them.
type messageInfo struct {
	name, contentType, schemaFormat, schemaName, schemaDigest string
}

func messageInfoFor(raw any, baseFile string, resolver *refResolver) messageInfo {
	originalRef := stringValue(mapValue(raw)["$ref"])
	message := resolver.resolve(raw, baseFile)
	if oneOf := sliceValue(message.value["oneOf"]); len(oneOf) > 0 {
		message = resolver.resolve(oneOf[0], message.absFile)
	}
	name := stringValue(message.value["name"])
	if name == "" {
		name = refTail(message.ref)
	}
	if name == "" {
		name = refTail(originalRef)
	}
	info := messageInfo{
		name: name, contentType: stringValue(message.value["contentType"]),
		schemaFormat: stringValue(message.value["schemaFormat"]),
	}
	payload := resolver.resolve(message.value["payload"], message.absFile)
	if len(payload.value) == 0 || info.name == "" {
		return info
	}
	// encoding/json orders map keys, giving equivalent YAML and JSON payloads the
	// same stable digest while keeping schemas with the same name but different
	// definitions distinct during cross-file contract deduplication.
	if normalized, err := json.Marshal(payload.value); err == nil {
		sum := sha256.Sum256(normalized)
		info.schemaDigest = hex.EncodeToString(sum[:])
	}
	rel, err := filepath.Rel(resolver.repoRoot, payload.absFile)
	if err == nil {
		info.schemaName = filepath.ToSlash(rel) + "#/messages/" + info.name
	}
	return info
}

func refTail(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return strings.ReplaceAll(strings.ReplaceAll(ref[i+1:], "~1", "/"), "~0", "~")
	}
	return ""
}
