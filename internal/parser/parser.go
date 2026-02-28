package parser

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// SwaggerSpec 从 YAML 解析出的精简 Swagger 结构
type SwaggerSpec struct {
	Actions []APIAction
}

// APIAction 一个 API 接口的描述
type APIAction struct {
	// 接口名称，如 CreateFlowEvidenceReport
	Name string
	// 接口中文名称
	ActionName string
	// 接口分类
	Category string
	// 接口描述
	Description string
	// 请求参数定义
	RequestParams []ParamDef
	// 响应参数定义
	ResponseParams []ParamDef
	// 错误码
	ErrorCodes []string
	// 接口示例（来自 x-tcapi-examples）
	Examples []map[string]interface{}
}

// ParamDef 参数定义
type ParamDef struct {
	Name        string
	Type        string     // string, integer, boolean, array, object
	Description string
	Required    bool
	Disabled    bool       // 是否已弃用
	Example     string
	Items       *ParamDef  // 当 Type 为 array 时，元素类型定义
	Properties  []ParamDef // 当 Type 为 object（$ref 解析后）时，子属性
	RefName     string     // 引用的定义名
}

// rawSwagger 用于 YAML 解析的原始结构
type rawSwagger struct {
	BodyDefinitions map[string]rawDefinition `yaml:"body_definitions"`
	Definitions     map[string]rawDefinition `yaml:"definitions"`
	Paths           map[string]rawPath       `yaml:"paths"`
	Info            map[string]interface{}   `yaml:"info"`
	Swagger         string                   `yaml:"swagger"`
}

type rawPath struct {
	Post rawOperation `yaml:"post"`
}

type rawOperation struct {
	Description string                   `yaml:"description"`
	OperationId string                   `yaml:"operationId"`
	Parameters  []rawParameter           `yaml:"parameters"`
	Responses   map[string]rawResponse   `yaml:"responses"`
	ActionName  string                   `yaml:"x-tcapi-action-name"`
	Category    string                   `yaml:"x-tcapi-category"`
	ErrorCodes  []string                 `yaml:"x-tcapi-errorcodes"`
	Examples    []map[string]interface{} `yaml:"x-tcapi-examples"`
}

type rawParameter struct {
	In     string    `yaml:"in"`
	Schema rawSchema `yaml:"schema"`
}

type rawResponse struct {
	Schema rawSchema `yaml:"schema"`
}

type rawSchema struct {
	Ref string `yaml:"$ref"`
}

type rawDefinition struct {
	Type       string                   `yaml:"type"`
	Properties map[string]rawProperty   `yaml:"properties"`
	Required   interface{}              `yaml:"required"` // 可能是 []string 或 []interface{}
	OutputReq  interface{}              `yaml:"x-tcapi-output-required"`
	Desc       string                   `yaml:"description"`
}

type rawProperty struct {
	Type        string      `yaml:"type"`
	Description string      `yaml:"description"`
	Example     interface{} `yaml:"example"`
	Disabled    bool        `yaml:"disabled"`
	Format      string      `yaml:"format"`
	Ref         string      `yaml:"$ref"`
	Items       *rawItems   `yaml:"items"`
	Nullable    bool        `yaml:"nullable"`
}

type rawItems struct {
	Ref  string `yaml:"$ref"`
	Type string `yaml:"type"`
}

// ParseSwaggerFile 解析 Swagger YAML 文件
func ParseSwaggerFile(path string) (*SwaggerSpec, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	var raw rawSwagger
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("解析 YAML 失败: %w", err)
	}

	spec := &SwaggerSpec{}

	// 合并 body_definitions 和 definitions
	allDefs := make(map[string]rawDefinition)
	for k, v := range raw.Definitions {
		allDefs[k] = v
	}
	for k, v := range raw.BodyDefinitions {
		allDefs[k] = v
	}

	// 遍历 paths，为每个接口创建 APIAction
	for pathStr, pathItem := range raw.Paths {
		op := pathItem.Post
		if op.OperationId == "" {
			continue
		}

		action := APIAction{
			Name:        op.OperationId,
			ActionName:  op.ActionName,
			Category:    op.Category,
			Description: cleanDescription(op.Description),
			ErrorCodes:  op.ErrorCodes,
			Examples:    op.Examples,
		}

		// 解析请求参数
		reqDefName := op.OperationId + "Request"
		if len(op.Parameters) > 0 && op.Parameters[0].Schema.Ref != "" {
			reqDefName = extractRefName(op.Parameters[0].Schema.Ref)
		}
		if def, ok := allDefs[reqDefName]; ok {
			action.RequestParams = resolveDefinition(def, allDefs, 0)
		}

		// 解析响应参数
		respDefName := op.OperationId + "Response"
		if resp, ok := op.Responses["200"]; ok && resp.Schema.Ref != "" {
			respDefName = extractRefName(resp.Schema.Ref)
		}
		if def, ok := allDefs[respDefName]; ok {
			action.ResponseParams = resolveDefinition(def, allDefs, 0)
		}

		_ = pathStr
		spec.Actions = append(spec.Actions, action)
	}

	return spec, nil
}

// extractRefName 从 $ref 中提取定义名
// 例如 "#/body_definitions/CreateFlowEvidenceReportRequest" -> "CreateFlowEvidenceReportRequest"
func extractRefName(ref string) string {
	parts := strings.Split(ref, "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return ref
}

// resolveDefinition 解析一个定义，展开属性
func resolveDefinition(def rawDefinition, allDefs map[string]rawDefinition, depth int) []ParamDef {
	if depth > 5 {
		return nil // 防止循环引用
	}

	requiredSet := parseRequired(def.Required)
	var params []ParamDef

	for name, prop := range def.Properties {
		if prop.Disabled {
			continue // 跳过弃用字段
		}

		param := ParamDef{
			Name:        name,
			Type:        prop.Type,
			Description: cleanDescription(prop.Description),
			Required:    requiredSet[name],
			Disabled:    prop.Disabled,
			Example:     fmt.Sprintf("%v", prop.Example),
		}

		// 处理 $ref 引用（对象类型）
		if prop.Ref != "" {
			refName := extractRefName(prop.Ref)
			param.Type = "object"
			param.RefName = refName
			if refDef, ok := allDefs[refName]; ok {
				param.Properties = resolveDefinition(refDef, allDefs, depth+1)
			}
		}

		// 处理 array 类型中的 $ref
		if prop.Type == "array" && prop.Items != nil {
			if prop.Items.Ref != "" {
				refName := extractRefName(prop.Items.Ref)
				param.Items = &ParamDef{
					Type:    "object",
					RefName: refName,
				}
				if refDef, ok := allDefs[refName]; ok {
					param.Items.Properties = resolveDefinition(refDef, allDefs, depth+1)
				}
			} else {
				param.Items = &ParamDef{
					Type: prop.Items.Type,
				}
			}
		}

		params = append(params, param)
	}

	return params
}

// parseRequired 解析 required 字段
func parseRequired(req interface{}) map[string]bool {
	result := make(map[string]bool)
	if req == nil {
		return result
	}

	switch v := req.(type) {
	case []interface{}:
		for _, item := range v {
			if s, ok := item.(string); ok {
				result[s] = true
			}
		}
	case []string:
		for _, s := range v {
			result[s] = true
		}
	}
	return result
}

// cleanDescription 清理描述文本，去除 HTML/SVG 标签
func cleanDescription(desc string) string {
	if desc == "" {
		return ""
	}

	// 移除 SVG 内容
	for {
		start := strings.Index(desc, "<svg")
		if start == -1 {
			break
		}
		end := strings.Index(desc[start:], "</svg>")
		if end == -1 {
			desc = desc[:start]
			break
		}
		desc = desc[:start] + desc[start+end+6:]
	}

	// 简单清理 HTML 标签
	replacer := strings.NewReplacer(
		"<br/>", "\n",
		"<br>", "\n",
		"<br />", "\n",
		"</li>", "\n",
		"</ul>", "\n",
		"</p>", "\n",
	)
	desc = replacer.Replace(desc)

	// 移除剩余的 HTML 标签
	var result strings.Builder
	inTag := false
	for _, r := range desc {
		if r == '<' {
			inTag = true
			continue
		}
		if r == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(r)
		}
	}

	// 清理多余空行
	lines := strings.Split(result.String(), "\n")
	var cleaned []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}

	return strings.Join(cleaned, "\n")
}
