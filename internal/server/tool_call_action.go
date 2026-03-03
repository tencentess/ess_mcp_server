package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"ess_mcp_server/internal/config"
	"ess_mcp_server/internal/parser"
	"ess_mcp_server/internal/server/custom"
	"ess_mcp_server/internal/signer"

	"github.com/mark3labs/mcp-go/mcp"
)

// registerCallEssAction 注册 call_ess_action 工具
// 大模型收集好参数后，通过此工具执行实际的 API 调用
func (s *MCPServer) registerCallEssAction() {
	schemaBytes := json.RawMessage(`{
		"type": "object",
		"properties": {
			"action": {
				"type": "string",
				"description": "要调用的接口名称，例如 DescribeFlowInfo"
			},
			"params": {
				"type": "object",
				"description": "接口请求参数，具体字段请先通过 get_tool_schema 获取"
			}
		},
		"required": ["action"]
	}`)

	tool := mcp.NewToolWithRawSchema(
		"call_ess_action",
		"调用腾讯电子签 API 接口。使用前请先通过 get_tool_schema 获取接口参数说明，按 Schema 收集参数后再调用此工具。",
		schemaBytes,
	)

	s.server.AddTool(tool, func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := request.GetArguments()
		actionName, _ := args["action"].(string)
		if actionName == "" {
			return mcp.NewToolResultError("缺少必填参数 action（接口名称）"), nil
		}

		action, ok := s.actionMap[actionName]
		if !ok {
			// 尝试模糊匹配
			var suggestions []string
			lowerName := strings.ToLower(actionName)
			for name := range s.actionMap {
				if strings.Contains(strings.ToLower(name), lowerName) {
					suggestions = append(suggestions, name)
				}
			}
			if len(suggestions) > 0 {
				return mcp.NewToolResultError(fmt.Sprintf("未找到接口 '%s'，您是否想调用: %s", actionName, strings.Join(suggestions, ", "))), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("未找到接口 '%s'，请检查接口名称是否正确", actionName)), nil
		}

		// 提取业务参数
		params, _ := args["params"].(map[string]interface{})
		if params == nil {
			params = make(map[string]interface{})
		}

		return s.executeAPICall(ctx, action, params)
	})
}

// executeAPICall 执行实际的 API 调用
func (s *MCPServer) executeAPICall(ctx context.Context, action parser.APIAction, params map[string]interface{}) (*mcp.CallToolResult, error) {
	// 如果存在定制化处理器，在调用 API 前对参数进行预处理
	if c := custom.Get(action.Name); c != nil {
		var err error
		params, err = c.PreprocessParams(params)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("参数预处理失败: %v", err)), nil
		}
	}

	if config.IsDebug() {
		js, _ := json.Marshal(params)
		config.Log(ctx, "executeAPICall 原始参数: %s", string(js))
	}

	// 从 context 中提取通过 HTTP Headers 传入的凭证信息
	secretId, _ := ctx.Value(ctxSecretId).(string)
	secretKey, _ := ctx.Value(ctxSecretKey).(string)
	env, _ := ctx.Value(ctxEnv).(string)
	userId, _ := ctx.Value(ctxUserId).(string)

	// 如果 HTTP Headers 没有传递凭证，则从 config.yaml 的默认配置中读取
	if secretId == "" && secretKey == "" && env == "" {
		secretId = s.cfg.Credentials.SecretId
		secretKey = s.cfg.Credentials.SecretKey
		env = s.cfg.Credentials.Env
		config.Log(ctx, "HTTP Headers 未传递凭证，使用 config.yaml 中的默认配置")
	}

	// 如果 HTTP Headers 未传递 UserId，则从 config.yaml 的默认配置中读取
	if userId == "" {
		userId = s.cfg.Credentials.UserId
	}

	// 如果有 UserId，自动注入到 Operator.UserId（不覆盖用户已显式传递的值）
	if userId != "" {
		injectOperatorUserId(params, userId)
		config.Log(ctx, "自动注入 Operator.UserId: %s", userId)
	}

	if secretId == "" || secretKey == "" {
		return mcp.NewToolResultError("缺少凭证信息: 请通过 HTTP 请求头（X-Secret-Id / X-Secret-Key）传递，或在 config.yaml 的 credentials 中配置默认值"), nil
	}

	if env == "" {
		return mcp.NewToolResultError("缺少环境信息: 请通过 HTTP 请求头（X-Env）传递（可选值: test / online），或在 config.yaml 的 credentials 中配置默认值"), nil
	}

	config.Log(ctx, "凭证信息: secretId=%s, env=%s", secretId, env)

	// 根据环境和接口名称自动选择 Endpoint
	endpoint, err := s.cfg.GetEndpoint(env, action.Name)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("获取 Endpoint 失败: %v", err)), nil
	}

	// 将业务参数序列化为 JSON
	payload, err := json.Marshal(params)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("参数序列化失败: %v", err)), nil
	}

	config.Log(ctx, "调用接口 %s, Endpoint: %s, 请求参数: %s", action.Name, endpoint, string(payload))

	// 构建签名请求
	signReq := signer.TC3SignRequest{
		SecretId:  secretId,
		SecretKey: secretKey,
		Service:   s.cfg.API.Service,
		Host:      endpoint,
		Action:    action.Name,
		Version:   s.cfg.API.APIVersion,
		Region:    "",
		Payload:   string(payload),
		Timestamp: time.Now().Unix(),
	}

	httpReq, err := signer.SignAndBuildRequest(signReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("签名失败: %v", err)), nil
	}
	httpReq = httpReq.WithContext(ctx)

	// 发送请求
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("请求腾讯云 API 失败: %v", err)), nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("读取响应失败: %v", err)), nil
	}

	config.Log(ctx, "接口 %s 响应状态: %d", action.Name, resp.StatusCode)

	// 解析响应，检查是否有错误
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return mcp.NewToolResultText(string(body)), nil
	}

	// 格式化 JSON 输出
	prettyJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return mcp.NewToolResultText(string(body)), nil
	}

	// 检查响应中的错误信息
	if response, ok := result["Response"].(map[string]interface{}); ok {
		if errInfo, ok := response["Error"].(map[string]interface{}); ok {
			errCode, _ := errInfo["Code"].(string)
			errMsg, _ := errInfo["Message"].(string)
			errResult := fmt.Sprintf("API 错误 [%s]: %s\n\n完整响应:\n%s", errCode, errMsg, string(prettyJSON))
			config.Log(ctx, "[响应->客户端] call_ess_action(%s) 返回错误:\n%s", action.Name, errResult)
			return mcp.NewToolResultError(errResult), nil
		}
	}

	// API 调用成功，执行后处理（如清理临时文件）
	if c := custom.Get(action.Name); c != nil {
		if err := c.PostprocessParams(params); err != nil {
			log.Printf("[警告] 接口 %s 后处理失败: %v", action.Name, err)
		}
	}

	config.Log(ctx, "[响应->客户端] call_ess_action(%s) 返回成功, 长度: %d 字节\n%s", action.Name, len(prettyJSON), string(prettyJSON))
	return mcp.NewToolResultText(string(prettyJSON)), nil
}

// injectOperatorUserId 自动注入操作人 UserId 到请求参数中
// 大多数接口使用 Operator.UserId，但 UploadFiles 等接口使用 Caller.OperatorId
// 如果 params 中已存在对应字段且非空，则不覆盖（用户显式传递优先）
func injectOperatorUserId(params map[string]interface{}, userId string) {
	// 如果 params 中已有 Operator，则注入 UserId
	if _, ok := params["Operator"]; ok {
		injectNestedField(params, "Operator", "UserId", userId)
	}

	// 如果 params 中已有 Caller，则注入 OperatorId（UploadFiles 等接口使用此字段）
	if _, ok := params["Caller"]; ok {
		injectNestedField(params, "Caller", "OperatorId", userId)
	}
}

// injectNestedField 向 params[parentKey][childKey] 注入值
// 如果 parentKey 不存在则创建，如果 childKey 已有非空值则不覆盖
func injectNestedField(params map[string]interface{}, parentKey, childKey, value string) {
	parent, ok := params[parentKey].(map[string]interface{})
	if !ok {
		// 父节点不存在或类型不对，创建新的
		parent = make(map[string]interface{})
		params[parentKey] = parent
	}

	// 仅在目标字段为空时注入
	if existing, ok := parent[childKey].(string); !ok || existing == "" {
		parent[childKey] = value
	}
}
