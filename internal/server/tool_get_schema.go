package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"ess_mcp_server/internal/config"
	"ess_mcp_server/internal/parser"
	"ess_mcp_server/internal/server/custom"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerGetToolSchema 注册 get_tool_schema 工具
// 大模型选择要调用的接口后，通过此工具获取该接口完整的 JSON Schema 和参数详情
func (s *MCPServer) registerGetToolSchema() {
	schemaBytes := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action_name": {
				"type": "string",
				"description": "要查询的接口名称，例如 DescribeFlowInfo、CreateSchemeUrl 等"
			}
		},
		"required": ["action_name"]
	}`)

	tool := mcp.NewToolWithRawSchema(
		"get_tool_schema",
		"获取指定接口的完整参数 Schema。在调用 call_ess_action 之前，请先使用此工具获取接口的详细参数说明，包括每个参数的类型、描述、是否必填等信息。",
		schemaBytes,
	)

	s.server.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		actionName, _ := args["action_name"].(string)
		if actionName == "" {
			return mcp.NewToolResultError("缺少必填参数 action_name"), nil
		}

		action, ok := s.actionMap[actionName]
		if !ok {
			// 尝试模糊匹配，给出建议
			var suggestions []string
			lowerName := strings.ToLower(actionName)
			for name := range s.actionMap {
				if strings.Contains(strings.ToLower(name), lowerName) {
					suggestions = append(suggestions, name)
				}
			}
			if len(suggestions) > 0 {
				return mcp.NewToolResultError(fmt.Sprintf("未找到接口 '%s'，您是否想查找: %s", actionName, strings.Join(suggestions, ", "))), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("未找到接口 '%s'，请检查接口名称是否正确", actionName)), nil
		}

		// 构建完整的 Schema 信息返回给大模型
		result := s.buildFullSchemaInfo(action)
		config.Log(ctx, "[响应->客户端] get_tool_schema(%s) 返回内容长度: %d 字节\n%s", actionName, len(result), result)
		return mcp.NewToolResultText(result), nil
	})
}

// buildFullSchemaInfo 构建精简的接口 Schema 信息
// 优化策略：
// 1. 从示例中提取实际使用的参数键名集合，只输出示例中出现过且非零值的参数
// 2. 过滤已弃用参数
// 3. 限制递归深度和描述长度
// 4. 如果没有示例，则回退到输出所有参数（保持兼容）
func (s *MCPServer) buildFullSchemaInfo(action parser.APIAction) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("# 接口: %s\n\n", action.Name))

	// 输出接口详细描述（使用 DescMaxLenLong 控制长度）
	if action.Description != "" {
		desc := truncateDesc(action.Description, s.cfg.Schema.DescMaxLenLong)
		sb.WriteString(fmt.Sprintf("## 接口说明\n%s\n\n", desc))
	}

	activeParams := filterActiveParams(action.RequestParams)

	// 如果存在定制化处理器，对参数定义进行定制
	if c := custom.Get(action.Name); c != nil {
		activeParams = c.CustomizeSchema(activeParams)
	}

	// 从示例中提取参数键名集合
	exampleKeys := extractExampleKeys(action.Examples)
	hasExampleFilter := len(exampleKeys) > 0

	// 如果接口有定制化处理器，将定制新增的字段也加入 exampleKeys，避免被示例过滤掉
	if c := custom.Get(action.Name); c != nil && hasExampleFilter {
		addCustomizedKeys(activeParams, exampleKeys)
	}

	sb.WriteString("## 请求参数说明\n")
	sb.WriteString("调用 call_ess_action 时，请将以下参数放入 params 对象中。\n")
	if hasExampleFilter {
		sb.WriteString("（以下仅列出示例中使用过的参数，如需了解全部参数请告知）\n")
	}
	sb.WriteString("\n")

	// 输出参数详细说明（带示例过滤）
	writeParamDetails(&sb, activeParams, "", 0, exampleKeys, s.cfg.Schema.DescMaxLenMedium, s.cfg.Schema.SchemaMaxDetailDepth)

	return sb.String()
}

// extractExampleKeys 从接口示例的 input 中提取所有非零值的参数键名路径集合
// 示例 input 格式: "POST / HTTP/1.1\nHost: ...\n\n{...json body...}"
// 返回的集合中键名格式为: "FlowId", "Operator.UserId", "FlowInfos[].FlowId" 等
func extractExampleKeys(examples []map[string]interface{}) map[string]bool {
	keys := make(map[string]bool)

	for _, ex := range examples {
		input, ok := ex["input"].(string)
		if !ok || input == "" {
			continue
		}

		// 跳过错误示例（标题中包含"错误"的示例）
		if title, ok := ex["title"].(string); ok && strings.Contains(title, "错误") {
			continue
		}

		// 从 HTTP 请求格式中提取 JSON body（在 \n\n 之后）
		jsonBody := extractJSONFromInput(input)
		if jsonBody == "" {
			continue
		}

		// 解析 JSON 并递归提取键名
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(jsonBody), &parsed); err != nil {
			continue
		}

		collectNonZeroKeys(parsed, "", keys)
	}

	return keys
}

// extractJSONFromInput 从示例的 input 字符串中提取 JSON body
// input 格式: "POST / HTTP/1.1\nHost: ...\nContent-Type: ...\n\n{...}"
func extractJSONFromInput(input string) string {
	// 找到空行后面的 JSON 内容
	idx := strings.Index(input, "\n\n")
	if idx < 0 {
		// 尝试直接查找第一个 {
		braceIdx := strings.Index(input, "{")
		if braceIdx >= 0 {
			return input[braceIdx:]
		}
		return ""
	}
	body := strings.TrimSpace(input[idx+2:])
	if len(body) > 0 && body[0] == '{' {
		return body
	}
	return ""
}

// collectNonZeroKeys 递归收集 JSON 对象中所有非零值的键名路径
// 零值包括: null, "", 0, false, 空数组[], 空对象{}
func collectNonZeroKeys(obj map[string]interface{}, prefix string, keys map[string]bool) {
	for k, v := range obj {
		fullKey := k
		if prefix != "" {
			fullKey = prefix + "." + k
		}

		if isZeroValue(v) {
			continue
		}

		// 当前键名标记为存在
		keys[fullKey] = true
		// 同时标记所有父级路径（确保父级对象也会被输出）
		markParentKeys(fullKey, keys)

		// 递归处理嵌套对象
		switch val := v.(type) {
		case map[string]interface{}:
			collectNonZeroKeys(val, fullKey, keys)
		case []interface{}:
			// 对数组中的对象元素，用 key[]. 前缀收集子键
			for _, item := range val {
				if itemObj, ok := item.(map[string]interface{}); ok {
					collectNonZeroKeys(itemObj, fullKey+"[]", keys)
				}
			}
		}
	}
}

// markParentKeys 标记一个键路径的所有父级路径
// 例如 "Operator.UserId" 会标记 "Operator"
// "FlowInfos[].Components[].Name" 会标记 "FlowInfos[]", "FlowInfos[].Components[]", "FlowInfos"
func markParentKeys(fullKey string, keys map[string]bool) {
	for {
		// 先尝试去掉 [] 后缀
		if strings.HasSuffix(fullKey, "[]") {
			parent := fullKey[:len(fullKey)-2]
			keys[parent] = true
			fullKey = parent
			continue
		}
		// 再尝试去掉最后一段 .xxx
		lastDot := strings.LastIndex(fullKey, ".")
		if lastDot < 0 {
			break
		}
		fullKey = fullKey[:lastDot]
		keys[fullKey] = true
	}
}

// isZeroValue 判断一个 JSON 值是否为零值
func isZeroValue(v interface{}) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case float64:
		return val == 0
	case bool:
		return !val
	case []interface{}:
		return len(val) == 0
	case map[string]interface{}:
		return len(val) == 0
	}
	return false
}

// writeParamDetails 递归写入参数详细说明（带深度限制、弃用过滤和示例过滤）
// exampleKeys 为 nil 或空时输出全部参数，否则只输出示例中出现的参数
func writeParamDetails(sb *strings.Builder, params []parser.ParamDef, prefix string, depth int, exampleKeys map[string]bool, descMaxLenMedium int, schemaMaxDetailDepth int) {
	for _, param := range params {
		// 跳过已弃用的参数
		if param.Disabled {
			continue
		}

		fullName := prefix + param.Name

		// 如果有示例过滤，只输出示例中出现过的参数
		if len(exampleKeys) > 0 && !exampleKeys[fullName] {
			continue
		}

		requiredTag := ""
		if param.Required {
			requiredTag = " **[必填]**"
		}

		// 截断过长描述（custom 定制的字段跳过截断）
		desc := param.Description
		if !param.SkipTruncate {
			desc = truncateDesc(desc, descMaxLenMedium)
		}
		sb.WriteString(fmt.Sprintf("- `%s` (%s)%s: %s\n", fullName, param.Type, requiredTag, desc))

		// 超过最大深度时，不再递归展开子参数，仅提示存在嵌套
		if depth >= schemaMaxDetailDepth {
			if len(param.Properties) > 0 || (param.Items != nil && len(param.Items.Properties) > 0) {
				sb.WriteString(fmt.Sprintf("  > （嵌套对象，包含更多子参数，此处省略。如需了解请单独查询）\n"))
			}
			continue
		}

		// 递归展示子参数
		if len(param.Properties) > 0 {
			writeParamDetails(sb, filterActiveParams(param.Properties), fullName+".", depth+1, exampleKeys, descMaxLenMedium, schemaMaxDetailDepth)
		}
		if param.Items != nil && len(param.Items.Properties) > 0 {
			writeParamDetails(sb, filterActiveParams(param.Items.Properties), fullName+"[].", depth+1, exampleKeys, descMaxLenMedium, schemaMaxDetailDepth)
		}
	}
}

// buildFullJSONSchema 构建完整的 JSON Schema（不限制嵌套深度），用于 get_tool_schema 返回
func buildFullJSONSchema(params []parser.ParamDef, descMaxLenMedium int) json.RawMessage {
	schema := map[string]interface{}{
		"type": "object",
	}

	properties := make(map[string]interface{})
	var required []string

	for _, param := range params {
		if param.Disabled {
			continue
		}
		propSchema := paramToFullSchema(param, 0, descMaxLenMedium)
		if propSchema == nil {
			continue
		}
		properties[param.Name] = propSchema
		if param.Required {
			required = append(required, param.Name)
		}
	}

	schema["properties"] = properties
	if len(required) > 0 {
		schema["required"] = required
	}

	data, _ := json.Marshal(schema)
	return data
}

// paramToFullSchema 将参数定义转为完整的 JSON Schema（最大嵌套深度3层，控制体积）
func paramToFullSchema(param parser.ParamDef, depth int, descMaxLenMedium int) map[string]interface{} {
	if depth > 3 {
		desc := param.Description
		if !param.SkipTruncate {
			desc = truncateDesc(desc, descMaxLenMedium)
		}
		return map[string]interface{}{"type": "object", "description": desc}
	}

	// 跳过已弃用的参数（返回空标记）
	if param.Disabled {
		return nil
	}

	schema := make(map[string]interface{})
	if param.SkipTruncate {
		schema["description"] = param.Description
	} else {
		schema["description"] = truncateDesc(param.Description, descMaxLenMedium)
	}

	switch param.Type {
	case "string":
		schema["type"] = "string"
	case "integer":
		schema["type"] = "integer"
	case "boolean":
		schema["type"] = "boolean"
	case "array":
		schema["type"] = "array"
		if param.Items != nil {
			if len(param.Items.Properties) > 0 {
				itemSchema := map[string]interface{}{
					"type": "object",
				}
				itemProps := make(map[string]interface{})
				var itemRequired []string
				for _, p := range param.Items.Properties {
					if p.Disabled {
						continue
					}
					propSchema := paramToFullSchema(p, depth+1, descMaxLenMedium)
					if propSchema != nil {
						itemProps[p.Name] = propSchema
						if p.Required {
							itemRequired = append(itemRequired, p.Name)
						}
					}
				}
				itemSchema["properties"] = itemProps
				if len(itemRequired) > 0 {
					itemSchema["required"] = itemRequired
				}
				schema["items"] = itemSchema
			} else {
				schema["items"] = map[string]interface{}{
					"type": param.Items.Type,
				}
			}
		}
	case "object":
		schema["type"] = "object"
		if len(param.Properties) > 0 {
			props := make(map[string]interface{})
			var objRequired []string
			for _, p := range param.Properties {
				if p.Disabled {
					continue
				}
				propSchema := paramToFullSchema(p, depth+1, descMaxLenMedium)
				if propSchema != nil {
					props[p.Name] = propSchema
					if p.Required {
						objRequired = append(objRequired, p.Name)
					}
				}
			}
			schema["properties"] = props
			if len(objRequired) > 0 {
				schema["required"] = objRequired
			}
		}
	default:
		schema["type"] = "string"
	}

	return schema
}

// addCustomizedKeys 扫描定制化后的参数列表，将不在 exampleKeys 中的新增字段加入
// 这样定制化处理器新增的字段（如 FileUrl、FilePath）不会被示例过滤掉
func addCustomizedKeys(params []parser.ParamDef, exampleKeys map[string]bool) {
	addCustomizedKeysRecursive(params, "", exampleKeys)
}

func addCustomizedKeysRecursive(params []parser.ParamDef, prefix string, exampleKeys map[string]bool) {
	for _, p := range params {
		fullName := prefix + p.Name
		// 如果这个字段不在 exampleKeys 中，说明是定制新增的，需要加入
		if !exampleKeys[fullName] {
			exampleKeys[fullName] = true
			// 同时标记父级路径
			markParentKeys(fullName, exampleKeys)
		}
		// 递归处理子属性
		if len(p.Properties) > 0 {
			addCustomizedKeysRecursive(p.Properties, fullName+".", exampleKeys)
		}
		if p.Items != nil && len(p.Items.Properties) > 0 {
			addCustomizedKeysRecursive(p.Items.Properties, fullName+"[].", exampleKeys)
		}
	}
}
