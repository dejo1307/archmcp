// Package asyncapiextractor reads AsyncAPI contracts as messaging topic facts.
package asyncapiextractor

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/enola-labs/enola/internal/facts"
	"gopkg.in/yaml.v3"
)

// Extractor extracts channels and operations from AsyncAPI 2.x and 3.x specs.
// It walks the repository itself because YAML and JSON are excluded from the
// engine's normal source-file walk.
type Extractor struct{}

func New() *Extractor             { return &Extractor{} }
func (e *Extractor) Name() string { return "asyncapi" }

func (e *Extractor) Detect(repoPath string) (bool, error) {
	found := false
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || found {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if isCandidate(path) && hasAsyncAPIRoot(path) {
			found = true
		}
		return nil
	})
	return found, err
}

func (e *Extractor) Extract(ctx context.Context, repoPath string, _ []string) ([]facts.Fact, error) {
	var result []facts.Fact
	err := filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !isCandidate(path) || !hasAsyncAPIContent(path) {
			return nil
		}
		rel, relErr := filepath.Rel(repoPath, path)
		if relErr != nil {
			return nil
		}
		ff, parseErr := parseFile(path, filepath.ToSlash(rel))
		if parseErr != nil {
			log.Printf("[asyncapi-extractor] skipping %s: %v", rel, parseErr)
			return nil
		}
		result = append(result, ff...)
		return nil
	})
	return result, err
}

func parseFile(absPath, relFile string) ([]facts.Fact, error) {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("parsing yaml/json: %w", err)
	}
	version := stringValue(doc["asyncapi"])
	if version == "" {
		return nil, fmt.Errorf("not an AsyncAPI spec (missing asyncapi field)")
	}

	channels := mapValue(doc["channels"])
	servers := serverProtocols(mapValue(doc["servers"]))
	defaultContentType := stringValue(doc["defaultContentType"])
	var operations []operation
	if strings.HasPrefix(version, "3.") {
		operations = operationsV3(doc, channels)
	} else {
		operations = operationsV2(channels)
	}

	specDir := filepath.ToSlash(filepath.Dir(relFile))
	seen := map[string]bool{}
	result := make([]facts.Fact, 0, len(operations))
	for _, op := range operations {
		if op.channel == "" || op.role == "" {
			continue
		}
		key := op.channel + "\x00" + op.role + "\x00" + op.id
		if seen[key] {
			continue
		}
		seen[key] = true
		props := map[string]any{
			"storage_kind": facts.StorageKindTopic, "messaging_role": op.role,
			"asyncapi_action": op.action, "asyncapi_version": version,
			"source": "asyncapi", "language": "asyncapi", "spec_file": relFile,
		}
		if protocol := protocolFor(op.serverNames, servers); protocol != "" {
			props["messaging"] = protocol
		}
		optional(props, "operationId", op.id)
		optional(props, "summary", op.summary)
		optional(props, "description", op.description)
		if len(op.tags) > 0 {
			props["tags"] = op.tags
		}
		optional(props, "message", op.message)
		contentType := op.contentType
		if contentType == "" {
			contentType = defaultContentType
		}
		optional(props, "content_type", contentType)
		result = append(result, facts.Fact{
			Kind: facts.KindStorage, Name: op.channel, File: relFile, Props: props,
			Relations: []facts.Relation{{Kind: facts.RelDeclares, Target: specDir}},
		})
	}
	return result, nil
}

type operation struct {
	channel, role, action, id, summary, description, message, contentType string
	tags, serverNames                                                     []string
}

func operationsV2(channels map[string]any) []operation {
	var out []operation
	for channelName, raw := range channels {
		channel := mapValue(raw)
		serverNames := stringList(channel["servers"])
		for _, action := range []string{"publish", "subscribe"} {
			opMap := mapValue(channel[action])
			if len(opMap) == 0 {
				continue
			}
			role := facts.MessagingRoleProducer
			if action == "subscribe" {
				role = facts.MessagingRoleConsumer
			}
			out = append(out, makeOperation(channelName, role, action, opMap, serverNames))
		}
	}
	return out
}

func operationsV3(doc, channels map[string]any) []operation {
	var out []operation
	for operationID, raw := range mapValue(doc["operations"]) {
		opMap := mapValue(raw)
		action := strings.ToLower(stringValue(opMap["action"]))
		role := ""
		switch action {
		case "send":
			role = facts.MessagingRoleProducer
		case "receive":
			role = facts.MessagingRoleConsumer
		}
		channelRef := mapValue(opMap["channel"])
		channelKey := localRefName(stringValue(channelRef["$ref"]), "channels")
		channel := channelRef
		if channelKey != "" {
			channel = mapValue(channels[channelKey])
		}
		channelName := stringValue(channel["address"])
		if channelName == "" {
			channelName = channelKey
		}
		op := makeOperation(channelName, role, action, opMap, refNames(channel["servers"], "servers"))
		if op.id == "" {
			op.id = operationID
		}
		out = append(out, op)
	}
	return out
}

func makeOperation(channel, role, action string, raw map[string]any, servers []string) operation {
	op := operation{channel: channel, role: role, action: action, id: stringValue(raw["operationId"]),
		summary: stringValue(raw["summary"]), description: stringValue(raw["description"]),
		tags: tagNames(raw["tags"]), serverNames: servers}
	op.message, op.contentType = messageMetadata(raw)
	return op
}

func messageMetadata(op map[string]any) (string, string) {
	var raw any = op["message"]
	if raw == nil {
		if messages := sliceValue(op["messages"]); len(messages) > 0 {
			raw = messages[0]
		}
	}
	message := mapValue(raw)
	if oneOf := sliceValue(message["oneOf"]); len(oneOf) > 0 {
		message = mapValue(oneOf[0])
	}
	name := stringValue(message["name"])
	if name == "" {
		name = localRefTail(stringValue(message["$ref"]))
	}
	return name, stringValue(message["contentType"])
}

func serverProtocols(raw map[string]any) map[string]string {
	out := make(map[string]string, len(raw))
	for name, value := range raw {
		out[name] = strings.ToLower(stringValue(mapValue(value)["protocol"]))
	}
	return out
}

func protocolFor(names []string, protocols map[string]string) string {
	for _, name := range names {
		if protocols[name] != "" {
			return protocols[name]
		}
	}
	unique := map[string]bool{}
	for _, protocol := range protocols {
		if protocol != "" {
			unique[protocol] = true
		}
	}
	if len(unique) == 1 {
		for protocol := range unique {
			return protocol
		}
	}
	return ""
}

func tagNames(raw any) []string {
	var out []string
	for _, item := range sliceValue(raw) {
		if name := stringValue(mapValue(item)["name"]); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func refNames(raw any, section string) []string {
	var out []string
	for _, item := range sliceValue(raw) {
		if name := localRefName(stringValue(mapValue(item)["$ref"]), section); name != "" {
			out = append(out, name)
		}
	}
	return out
}

func localRefName(ref, section string) string {
	prefix := "#/" + section + "/"
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.ReplaceAll(strings.ReplaceAll(strings.TrimPrefix(ref, prefix), "~1", "/"), "~0", "~")
}

func localRefTail(ref string) string {
	if i := strings.LastIndex(ref, "/"); i >= 0 {
		return ref[i+1:]
	}
	return ""
}
func mapValue(v any) map[string]any { m, _ := v.(map[string]any); return m }
func sliceValue(v any) []any        { s, _ := v.([]any); return s }
func stringValue(v any) string      { s, _ := v.(string); return s }
func stringList(v any) []string {
	var out []string
	for _, item := range sliceValue(v) {
		if s := stringValue(item); s != "" {
			out = append(out, s)
		}
	}
	return out
}
func optional(props map[string]any, key, value string) {
	if value != "" {
		props[key] = value
	}
}

func isCandidate(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func hasAsyncAPIContent(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 4096)
	n, _ := f.Read(buf)
	content := string(buf[:n])
	return strings.Contains(content, "asyncapi:") || strings.Contains(content, `"asyncapi"`)
}

func hasAsyncAPIRoot(path string) bool {
	if !hasAsyncAPIContent(path) {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var probe struct {
		AsyncAPI string `yaml:"asyncapi"`
	}
	return yaml.Unmarshal(data, &probe) == nil && probe.AsyncAPI != ""
}

func skipDir(name string) bool {
	switch name {
	case "vendor", "node_modules", ".git", ".enola", "backstage", "tmp", "log", "build", ".build", ".gradle", "testdata":
		return true
	default:
		return false
	}
}
