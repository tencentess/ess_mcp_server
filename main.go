package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"
	"ess_mcp_server/internal/config"
	"ess_mcp_server/internal/parser"
	"ess_mcp_server/internal/server"
)

//本机的名字
var mainHostName string

//初始化日志
func init() {
	hostName, err := os.Hostname()
	if err != nil {
		panic(err)
	}
	mainHostName = hostName
	log.SetFlags(log.Lshortfile | log.LstdFlags | log.Lmicroseconds)
	logOut := &lumberjack.Logger{
		Filename:   "./log/" + mainHostName + ".log",
		MaxSize:    500,
		MaxBackups: 10,
		Compress:   true,
		LocalTime:  true,
	}
	log.SetOutput(logOut)
}

func main() {
	execPath, err := os.Executable()
	if err != nil {
		log.Printf("获取可执行文件路径失败: %v\n", err)
		os.Exit(1)
	}
	execDir := filepath.Dir(execPath)

	configPath := filepath.Join(execDir, "config.yaml")
	cfg, err := config.LoadFromFile(configPath)
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("配置加载成功: 端口=%s, 服务名=%s\n", cfg.Server.Port, cfg.Server.Name)

	yamlDir := filepath.Join(execDir, "yaml")

	// 从配置中读取 API 白名单
	allowedActions := make(map[string]bool)
	if len(cfg.API.LoadingAPIs) > 0 {
		for _, actionName := range cfg.API.LoadingAPIs {
			allowedActions[actionName] = true
		}
		fmt.Printf("从配置文件加载了 %d 个允许的 API 接口白名单\n", len(allowedActions))
	} else {
		fmt.Printf("未配置 API 白名单，将加载所有接口\n")
	}

	entries, err := os.ReadDir(yamlDir)
	if err != nil {
		log.Fatalf("读取 yaml 目录失败: %v", err)
	}

	// 根据配置的 service 名称确定 yaml 文件前缀过滤规则（如 ess_*.yaml 或 essbasic_*.yaml）
	yamlPrefix := strings.ToLower(cfg.API.Service) + "_"
	fmt.Printf("根据 service 配置 [%s]，只加载前缀为 [%s] 的 YAML 文件\n", cfg.API.Service, yamlPrefix)

	mergedSpec := &parser.SwaggerSpec{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		fileName := strings.ToLower(entry.Name())
		if !strings.HasSuffix(fileName, ".yaml") {
			continue
		}
		if !strings.HasPrefix(fileName, yamlPrefix) {
			config.Debug("跳过不匹配 service 前缀的文件: %s\n\n", entry.Name())
			continue
		}
		yamlPath := filepath.Join(yamlDir, entry.Name())
		config.Debug("正在解析 Swagger YAML 文件: %s", yamlPath)

		spec, err := parser.ParseSwaggerFile(yamlPath)
		if err != nil {
			fmt.Printf("解析 Swagger 文件 %s 失败: %v，跳过该文件\n", yamlPath, err)
			continue
		}
		config.Debug("从 %s 中解析到 %d 个 API 接口", yamlPath, len(spec.Actions))
		// 如果有白名单，则只加载白名单中的 action
		if len(allowedActions) > 0 {
			for _, action := range spec.Actions {
				if allowedActions[action.Name] {
					mergedSpec.Actions = append(mergedSpec.Actions, action)
				} else {
					config.Debug("跳过未在白名单中的 action: %s", action.Name)
				}
			}
		} else {
			mergedSpec.Actions = append(mergedSpec.Actions, spec.Actions...)
		}
	}

	if len(mergedSpec.Actions) == 0 {
		log.Printf("未解析到任何 API 接口，请检查 %s 目录下的 YAML 文件\n", yamlDir)
		os.Exit(1)
	}
	fmt.Printf("总共解析到 %d 个 API 接口\n", len(mergedSpec.Actions))

	// 创建并启动 MCP Server
	srv, err := server.NewMCPServer(mergedSpec, cfg)
	if err != nil {
		log.Printf("创建 MCP Server 失败: %v\n", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%s", cfg.Server.Port)
	fmt.Printf("MCP Server 启动中, 监听地址: %s\n", addr)

	if err := srv.Start(context.Background(), addr); err != nil {
		log.Printf("MCP Server 运行失败: %v\n", err)
		os.Exit(1)
	}
}
