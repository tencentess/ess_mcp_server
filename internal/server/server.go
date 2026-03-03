package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"ess_mcp_server/internal/config"
	"ess_mcp_server/internal/parser"
	"ess_mcp_server/internal/server/custom"

	mcpserver "github.com/mark3labs/mcp-go/server"
)

// HTTP Header 名称，用于从请求头中读取凭证信息
const (
	headerSecretId  = "X-Secret-Id"
	headerSecretKey = "X-Secret-Key"
	headerEnv       = "X-Env"
	headerUserId    = "X-User-Id"
)

// context key 类型，用于在 context 中存储凭证信息
type ctxKey string

const (
	ctxSecretId  ctxKey = "secretId"
	ctxSecretKey ctxKey = "secretKey"
	ctxEnv       ctxKey = "env"
	ctxUserId    ctxKey = "userId"
)

// MCPServer 封装 MCP 服务
type MCPServer struct {
	server     *mcpserver.MCPServer
	spec       *parser.SwaggerSpec
	cfg        *config.Config
	actionMap  map[string]parser.APIAction // 接口名 -> APIAction 的映射，用于O(1)查找
	fileServer *FileServer                 // 文件上传服务
}

// NewMCPServer 创建 MCP Server，采用按需加载架构：
// 1. 每个 API 接口注册为精简 tool（只有接口名+中文名+简短描述，无参数 Schema）
// 2. get_tool_schema 工具：获取指定接口的完整 JSON Schema
// 3. call_ess_action 工具：执行指定接口的 API 调用
func NewMCPServer(spec *parser.SwaggerSpec, cfg *config.Config) (*MCPServer, error) {
	s := mcpserver.NewMCPServer(
		cfg.Server.Name,
		cfg.Server.Version,
		mcpserver.WithToolCapabilities(true),
	)

	srv := &MCPServer{
		server:     s,
		spec:       spec,
		cfg:        cfg,
		actionMap:  make(map[string]parser.APIAction),
		fileServer: NewFileServer(),
	}

	// 构建 actionMap 并为每个 API 接口注册精简 tool
	for _, action := range spec.Actions {
		srv.actionMap[action.Name] = action
		srv.registerLightweightTool(action)
		config.Debug("已注册工具: %s (%s)", action.Name, action.ActionName)
	}

	// 注册 get_tool_schema 工具 —— 用于获取指定接口的完整 JSON Schema
	srv.registerGetToolSchema()

	// 注册 call_ess_action 工具 —— 用于执行 API 调用
	srv.registerCallEssAction()

	log.Printf("总共注册了 %d 个接口工具 + 2 个通用工具 (get_tool_schema, call_ess_action)", len(spec.Actions))

	return srv, nil
}

// truncateDesc 截断过长的描述信息（基于 rune，安全处理 UTF-8 多字节字符）
func truncateDesc(desc string, maxLen int) string {
	if runes := []rune(desc); len(runes) > maxLen {
		desc = string(runes[:maxLen]) + "..."
	}
	return desc
}

// filterActiveParams 过滤掉已弃用（Disabled）的参数
func filterActiveParams(params []parser.ParamDef) []parser.ParamDef {
	result := make([]parser.ParamDef, 0, len(params))
	for _, p := range params {
		if !p.Disabled {
			result = append(result, p)
		}
	}
	return result
}

// extractCredentials 从 HTTP 请求头中提取凭证信息并注入 context
func extractCredentials(ctx context.Context, r *http.Request) context.Context {
	// 生成请求级别的唯一 requestId，用于日志追踪
	ctx = config.WithRequestId(ctx, config.GenerateRequestId())

	if v := r.Header.Get(headerSecretId); v != "" {
		ctx = context.WithValue(ctx, ctxSecretId, v)
	}
	if v := r.Header.Get(headerSecretKey); v != "" {
		ctx = context.WithValue(ctx, ctxSecretKey, v)
	}
	if v := r.Header.Get(headerEnv); v != "" {
		ctx = context.WithValue(ctx, ctxEnv, v)
	}
	if v := r.Header.Get(headerUserId); v != "" {
		ctx = context.WithValue(ctx, ctxUserId, v)
	}
	return ctx
}

// Start 启动 MCP Server（Streamable HTTP 模式）
func (s *MCPServer) Start(ctx context.Context, addr string) error {
	// 初始化全局文件存储，通过 server_ip + port 组合生成对外基础 URL
	serverBaseUrl := fmt.Sprintf("http://%s:%s", s.cfg.Server.ServerIp, s.cfg.Server.Port)
	custom.InitFileStore(serverBaseUrl)

	streamableServer := mcpserver.NewStreamableHTTPServer(s.server,
		mcpserver.WithHTTPContextFunc(extractCredentials),
	)

	mux := http.NewServeMux()
	mux.Handle("/mcp", streamableServer)

	// 注册文件上传端点
	mux.HandleFunc("/upload", s.fileServer.HandleUpload)

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	// 用 Go 协程启动文件服务的日志提示
	log.Printf("MCP Server 启动于 %s (Streamable HTTP: /mcp, 文件上传: /upload)", addr)

	// 在独立协程中启动 HTTP 服务
	errCh := make(chan error, 1)
	go func() {
		log.Printf("[FileServer] 文件上传服务已在协程中启动，监听地址: %s/upload", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("[FileServer] 文件服务启动失败: %v", err)
			errCh <- err
		}
	}()

	// 短暂等待，检查是否立即启动失败
	select {
	case err := <-errCh:
		return fmt.Errorf("HTTP 服务启动失败: %w", err)
	case <-time.After(100 * time.Millisecond):
		// 启动成功，阻塞等待 context 取消
	}

	// 阻塞直到 context 被取消
	<-ctx.Done()
	log.Printf("MCP Server 正在关闭...")
	return srv.Close()
}
