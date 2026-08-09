package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGeneratedDefaultConfigMatchesDefaults(t *testing.T) {
	generated, err := generateDocumentedYAML(defaultConfig())
	if err != nil {
		t.Fatalf("generateDocumentedYAML() error: %v", err)
	}

	var decoded ProxyConfig
	if err := yaml.Unmarshal(generated, &decoded); err != nil {
		t.Fatalf("generated default config is not valid YAML: %v", err)
	}
	if !reflect.DeepEqual(&decoded, defaultConfig()) {
		t.Fatalf("generated config does not preserve defaults:\nwant: %#v\n got: %#v", defaultConfig(), &decoded)
	}
	if len(generated) < len(schemaDirective) || string(generated[:len(schemaDirective)]) != schemaDirective {
		t.Fatal("generated config is missing the YAML schema directive")
	}
	if !bytes.Contains(generated, []byte("# HTTP/HTTPS proxy listen address.")) {
		t.Fatal("generated config is missing field documentation")
	}
}

func TestGeneratedConfigSchemaContainsAllFields(t *testing.T) {
	generated, err := generateConfigSchema(defaultConfig())
	if err != nil {
		t.Fatalf("generateConfigSchema() error: %v", err)
	}

	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(generated, &schema); err != nil {
		t.Fatalf("generated config schema is not valid JSON: %v", err)
	}
	assertSchemaFieldsMatch(t, "root", schema.Properties, reflect.TypeOf(ProxyConfig{}))

	var rulesSchema struct {
		Items struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"items"`
	}
	if err := json.Unmarshal(schema.Properties["rules"], &rulesSchema); err != nil {
		t.Fatalf("generated rules schema is not valid JSON: %v", err)
	}
	assertSchemaFieldsMatch(t, "rule", rulesSchema.Items.Properties, reflect.TypeOf(RuleConfig{}))
}

func TestSaveDefaultConfigWritesTemplateAndSchema(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	defaults := defaultConfig()
	wantConfig, err := generateDocumentedYAML(defaults)
	if err != nil {
		t.Fatalf("generateDocumentedYAML() error: %v", err)
	}
	wantSchema, err := generateConfigSchema(defaults)
	if err != nil {
		t.Fatalf("generateConfigSchema() error: %v", err)
	}

	if err := saveDefaultConfig(configPath, defaults); err != nil {
		t.Fatalf("saveDefaultConfig() error: %v", err)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("reading saved config: %v", err)
	}
	if !reflect.DeepEqual(configData, wantConfig) {
		t.Fatal("saved config does not match generated config")
	}

	schemaData, err := os.ReadFile(filepath.Join(dir, configSchemaFilename))
	if err != nil {
		t.Fatalf("reading saved schema: %v", err)
	}
	if !reflect.DeepEqual(schemaData, wantSchema) {
		t.Fatal("saved schema does not match generated schema")
	}
}

func assertSchemaFieldsMatch(t *testing.T, schemaName string, properties map[string]json.RawMessage, configType reflect.Type) {
	t.Helper()

	fields := yamlFieldNames(configType)
	for field := range fields {
		if _, ok := properties[field]; !ok {
			t.Errorf("%s schema is missing YAML field %q", schemaName, field)
		}
	}
	for property := range properties {
		if _, ok := fields[property]; !ok {
			t.Errorf("%s schema contains unknown YAML field %q", schemaName, property)
		}
	}
}

func yamlFieldNames(configType reflect.Type) map[string]struct{} {
	fields := make(map[string]struct{})
	for _, field := range orderedYAMLStructFields(configType) {
		if name := yamlFieldName(field); name != "" {
			fields[name] = struct{}{}
		}
	}
	return fields
}
