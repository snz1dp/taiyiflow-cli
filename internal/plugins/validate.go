// Package plugins 提供插件开发套件（DevKit）核心能力：
// manifest schema 校验、插件工程脚手架生成、.plugin-dev.json 管理。
// schema 与宿主（taiyiflow-electron）共享同一份 plugin-manifest.schema.json，
// 保证「CLI 校验通过的就能被宿主加载」这条契约。
package plugins

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed schemas/plugin-manifest.schema.json
var manifestSchemaJSON string

// ValidationError 表示 manifest 校验失败的单条错误（字段路径 + 消息）。
// 报错格式对齐宿主 manifest-validator.js 的 `path: message` 风格。
type ValidationError struct {
	Path    string
	Message string
}

// Error 实现 error 接口（单条错误）。
func (e ValidationError) Error() string {
	return fmt.Sprintf("%s: %s", e.Path, e.Message)
}

// ValidateResult 表示校验结果：错误列表为空即通过。
type ValidateResult struct {
	Valid  bool
	Errors []ValidationError
}

// GetManifestSchema 返回编译后的 manifest schema（每次调用重新编译，
// 保证测试可覆盖编译错误路径；schema 内容由 go:embed 静态注入）。
// 注意：v6 的 AddResource 必须传 UnmarshalJSON 解码后的 any（map），
// 传字符串 / json.RawMessage 会被当作待校验数据触发 metaschema 校验。
func GetManifestSchema() (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(manifestSchemaJSON))
	if err != nil {
		return nil, fmt.Errorf("解析 manifest schema 失败: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	if err := compiler.AddResource("plugin-manifest.schema.json", doc); err != nil {
		return nil, fmt.Errorf("加载 manifest schema 失败: %w", err)
	}
	return compiler.Compile("plugin-manifest.schema.json")
}

// ValidateManifest 校验插件 manifest（JSON 对象）是否符合共享 schema。
// manifest 为已解析的 JSON 对象（map[string]any）；返回错误列表为空时 Valid 为 true。
func ValidateManifest(manifest any) ValidateResult {
	schema, err := GetManifestSchema()
	if err != nil {
		return ValidateResult{
			Valid: false,
			Errors: []ValidationError{{
				Path:    "(schema)",
				Message: err.Error(),
			}},
		}
	}
	if err := schema.Validate(manifest); err != nil {
		// santhosh-tekuri/jsonschema v6：ValidationError 用 BasicOutput() 输出结构化
		// 错误树（OutputUnit.InstanceLocation 为 JSON Pointer 字符串、Error.LocalizedString
		// 为错误描述）；递归展平为宿主风格 path: message
		var result ValidateResult
		if ve, ok := err.(*jsonschema.ValidationError); ok {
			collectOutputErrors(ve.BasicOutput(), &result.Errors)
		} else {
			result.Errors = append(result.Errors, ValidationError{
				Path:    "(root)",
				Message: err.Error(),
			})
		}
		return result
	}
	return ValidateResult{Valid: true}
}

// collectOutputErrors 递归展平 BasicOutput 错误树（跳过 valid 节点）。
func collectOutputErrors(unit *jsonschema.OutputUnit, errors *[]ValidationError) {
	if unit == nil || unit.Valid {
		return
	}
	if unit.Error != nil {
		*errors = append(*errors, ValidationError{
			Path:    normalizeJSONPointer(unit.InstanceLocation),
			Message: unit.Error.String(),
		})
	}
	for i := range unit.Errors {
		collectOutputErrors(&unit.Errors[i], errors)
	}
}

// ValidateManifestFile 校验指定路径的 plugin.json 文件。
// 返回校验结果与解析错误（文件不存在 / JSON 语法错误时 parseErr 非 nil）。
func ValidateManifestFile(manifestPath string) (ValidateResult, error) {
	content, err := readFileIfExists(manifestPath)
	if err != nil {
		return ValidateResult{}, fmt.Errorf("读取 %s 失败: %w", manifestPath, err)
	}
	if content == nil {
		return ValidateResult{}, fmt.Errorf("插件清单不存在: %s", manifestPath)
	}
	var manifest any
	if err := json.Unmarshal(content, &manifest); err != nil {
		return ValidateResult{}, fmt.Errorf("解析 %s 失败（JSON 语法错误）: %w", manifestPath, err)
	}
	return ValidateManifest(manifest), nil
}

// normalizeJSONPointer 把 jsonschema 的 InstanceLocation（JSON Pointer 形如
// /contributions/headerActions/0/id）转为宿主风格的 path（contributions.headerActions[0].id）。
func normalizeJSONPointer(pointer string) string {
	p := strings.TrimPrefix(pointer, "#")
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "(root)"
	}
	segments := strings.Split(p, "/")
	var parts []string
	for _, seg := range segments {
		seg = strings.ReplaceAll(seg, "~1", "/")
		seg = strings.ReplaceAll(seg, "~0", "~")
		if seg == "" {
			continue
		}
		// 纯数字段视为数组索引
		if isNumericSegment(seg) {
			parts = append(parts, fmt.Sprintf("[%s]", seg))
		} else {
			if len(parts) > 0 && !strings.HasPrefix(parts[len(parts)-1], "[") {
				parts = append(parts, "."+seg)
			} else {
				parts = append(parts, seg)
			}
		}
	}
	return strings.Join(parts, "")
}

// isNumericSegment 判断 JSON Pointer 段是否为纯数字（数组索引）。
func isNumericSegment(seg string) bool {
	if seg == "" {
		return false
	}
	for _, r := range seg {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
