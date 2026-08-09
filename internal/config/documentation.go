package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

const schemaDirective = "# yaml-language-server: $schema=./" + configSchemaFilename + "\n"

func generateDocumentedYAML(config *ProxyConfig) ([]byte, error) {
	var document yaml.Node
	if err := document.Encode(config); err != nil {
		return nil, err
	}
	if len(document.Content) == 0 {
		return nil, fmt.Errorf("encoded YAML document is empty")
	}

	annotateYAMLNode(&document, reflect.TypeOf(*config))

	var output bytes.Buffer
	output.WriteString(schemaDirective)
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	if err := encoder.Encode(&document); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func annotateYAMLNode(node *yaml.Node, configType reflect.Type) {
	configType = indirectType(configType)
	if node == nil {
		return
	}

	switch configType.Kind() {
	case reflect.Struct:
		if node.Kind != yaml.MappingNode {
			return
		}
		fields := yamlStructFields(configType)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			field, ok := fields[key.Value]
			if !ok {
				continue
			}
			key.HeadComment = field.Tag.Get("config_description")
			annotateYAMLNode(value, field.Type)
		}
	case reflect.Slice, reflect.Array:
		for _, item := range node.Content {
			annotateYAMLNode(item, configType.Elem())
		}
	}
}

func generateConfigSchema(config *ProxyConfig) ([]byte, error) {
	schema := schemaForType(reflect.TypeOf(*config), reflect.ValueOf(*config))
	schema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
	schema["$id"] = "https://github.com/Feverqwe/goProxy/goproxy.schema.json"
	schema["title"] = "GoProxy configuration"
	schema["description"] = "Configuration for the GoProxy HTTP, HTTPS, and SOCKS5 proxy server."

	data, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func schemaForType(configType reflect.Type, defaultValue reflect.Value) map[string]any {
	configType = indirectType(configType)
	defaultValue = indirectValue(defaultValue)

	switch configType.Kind() {
	case reflect.Struct:
		properties := make(map[string]any)
		var required []string
		for _, field := range orderedYAMLStructFields(configType) {
			name := yamlFieldName(field)
			if name == "" {
				continue
			}

			fieldDefault := reflect.Value{}
			if defaultValue.IsValid() && defaultValue.Kind() == reflect.Struct {
				fieldDefault = defaultValue.FieldByIndex(field.Index)
			}
			property := schemaForType(field.Type, fieldDefault)
			applySchemaTags(property, field)
			properties[name] = property
			if field.Tag.Get("config_required") == "true" {
				required = append(required, name)
			}
		}

		schema := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties":           properties,
		}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema

	case reflect.Map:
		schema := map[string]any{
			"type":                 "object",
			"additionalProperties": schemaForType(configType.Elem(), reflect.Value{}),
		}
		applyDefault(schema, defaultValue)
		return schema

	case reflect.Slice, reflect.Array:
		itemSchema := schemaForType(configType.Elem(), reflect.Value{})
		schema := map[string]any{"type": "array", "items": itemSchema}
		applyDefault(schema, defaultValue)
		return schema

	case reflect.String:
		schema := map[string]any{"type": "string"}
		applyDefault(schema, defaultValue)
		return schema

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		schema := map[string]any{"type": "integer"}
		applyDefault(schema, defaultValue)
		return schema

	case reflect.Float32, reflect.Float64:
		schema := map[string]any{"type": "number"}
		applyDefault(schema, defaultValue)
		return schema

	case reflect.Bool:
		schema := map[string]any{"type": "boolean"}
		applyDefault(schema, defaultValue)
		return schema
	default:
		return map[string]any{}
	}
}

func applySchemaTags(schema map[string]any, field reflect.StructField) {
	if description := field.Tag.Get("config_description"); description != "" {
		schema["description"] = description
	}
	if enum := splitTag(field.Tag.Get("config_enum"), ","); len(enum) > 0 {
		schema["enum"] = enum
	}
	if examples := splitTag(field.Tag.Get("config_examples"), "|"); len(examples) > 0 {
		schema["examples"] = examples
	}
	if minimum := field.Tag.Get("config_minimum"); minimum != "" {
		if value, err := strconv.Atoi(minimum); err == nil {
			schema["minimum"] = value
		}
	}
	if alternatives := splitTag(field.Tag.Get("config_item_any_of"), ","); len(alternatives) > 0 {
		if items, ok := schema["items"].(map[string]any); ok {
			anyOf := make([]any, 0, len(alternatives))
			for _, name := range alternatives {
				anyOf = append(anyOf, map[string]any{"required": []string{name}})
			}
			items["anyOf"] = anyOf
		}
	}
}

func applyDefault(schema map[string]any, value reflect.Value) {
	value = indirectValue(value)
	if value.IsValid() && value.CanInterface() {
		schema["default"] = value.Interface()
	}
}

func yamlStructFields(configType reflect.Type) map[string]reflect.StructField {
	fields := make(map[string]reflect.StructField)
	for _, field := range orderedYAMLStructFields(configType) {
		if name := yamlFieldName(field); name != "" {
			fields[name] = field
		}
	}
	return fields
}

func orderedYAMLStructFields(configType reflect.Type) []reflect.StructField {
	var fields []reflect.StructField
	for i := 0; i < configType.NumField(); i++ {
		field := configType.Field(i)
		if !field.IsExported() {
			continue
		}
		if hasYAMLOption(field, "inline") {
			fields = append(fields, orderedYAMLStructFields(indirectType(field.Type))...)
			continue
		}
		if yamlFieldName(field) != "" {
			fields = append(fields, field)
		}
	}
	return fields
}

func yamlFieldName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
	if name == "-" {
		return ""
	}
	if name != "" {
		return name
	}
	if field.Anonymous {
		return ""
	}
	runes := []rune(field.Name)
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func hasYAMLOption(field reflect.StructField, wanted string) bool {
	_, options, found := strings.Cut(field.Tag.Get("yaml"), ",")
	if !found {
		return false
	}
	for _, option := range strings.Split(options, ",") {
		if option == wanted {
			return true
		}
	}
	return false
}

func splitTag(value, separator string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, separator)
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func indirectType(value reflect.Type) reflect.Type {
	for value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	return value
}

func indirectValue(value reflect.Value) reflect.Value {
	for value.IsValid() && value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return reflect.Value{}
		}
		value = value.Elem()
	}
	return value
}
