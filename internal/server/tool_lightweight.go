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

// registerLightweightTool 为单个 API 接口注册精简 tool
func (s *MCPServer) registerLightweightTool(action parser.APIAction) {
	description := buildLightweightDescription(action, s.cfg.Schema.DescMaxLenShort)

	emptySchema := json.RawMessage(`{"type": "object", "properties": {}}`)

	tool := mcp.NewToolWithRawSchema(
		action.Name,
		description,
		emptySchema,
	)

	s.server.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		resultText := fmt.Sprintf(
			"⚠️ 请不要直接调用此工具。必需按以下步骤操作：\n"+
				"1. 先调用 get_tool_schema 工具，传入 action_name=\"%s\" 获取完整的参数说明\n"+
				"2. 根据返回的参数 Schema 收集用户信息\n"+
				"3. 调用 call_ess_action 工具，传入 action=\"%s\" 和 params 执行接口调用",
			action.Name, action.Name,
		)
		config.Log(ctx, "[响应->客户端] 工具 %s 被直接调用，返回引导信息:\n%s", action.Name, resultText)
		return mcp.NewToolResultText(resultText), nil
	})
}

// buildLightweightDescription 构建精简的工具描述
func buildLightweightDescription(action parser.APIAction, descMaxLenShort int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s]", action.ActionName))
	if action.Description != "" {
		desc := truncateDesc(action.Description, descMaxLenShort)
		sb.WriteString(" ")
		sb.WriteString(desc)
	}
	// 追加定制化描述后缀
	if suffix := custom.GetDescriptionSuffix(action.Name); suffix != "" {
		sb.WriteString(suffix)
	}
	return sb.String()
}
