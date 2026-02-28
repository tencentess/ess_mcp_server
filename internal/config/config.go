package config

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// globalDebug 全局 debug 标识，由 LoadFromFile 初始化
var globalDebug bool

// requestId context key 类型
type reqIdKeyType struct{}

var reqIdKey = reqIdKeyType{}

func init() {
	rand.Seed(time.Now().UnixNano())
}

// GenerateRequestId 生成一个 8 位的短随机请求 ID，用于日志追踪
func GenerateRequestId() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

// WithRequestId 将 requestId 注入到 context 中
func WithRequestId(ctx context.Context, requestId string) context.Context {
	return context.WithValue(ctx, reqIdKey, requestId)
}

// RequestIdFromCtx 从 context 中提取 requestId
func RequestIdFromCtx(ctx context.Context) string {
	if v, ok := ctx.Value(reqIdKey).(string); ok {
		return v
	}
	return "-"
}

// IsDebug 返回当前是否处于 debug 模式
func IsDebug() bool {
	return globalDebug
}

// Debug 仅在 debug 模式下打印日志（不带 requestId）
func Debug(format string, v ...interface{}) {
	if globalDebug {
		log.Printf(format, v...)
	}
}

// reqLogPrint 内部方法：将格式化后的消息中的换行符替换为空格，确保日志始终打印在一行内
func reqLogPrint(ctx context.Context, format string, v ...interface{}) {
	reqId := RequestIdFromCtx(ctx)
	msg := fmt.Sprintf(format, v...)
	msg = strings.ReplaceAll(msg, "\n", " ")
	for strings.Contains(msg, "  ") {
		msg = strings.ReplaceAll(msg, "  ", " ")
	}
	log.Printf("[%s] %s", reqId, msg)
}

// Log 带 requestId 前缀的 debug 日志打印（仅 debug 模式），多行内容会被压缩成一行
func Log(ctx context.Context, format string, v ...interface{}) {
	if globalDebug {
		reqLogPrint(ctx, format, v...)
	}
}

// CredentialsConfig 默认凭证配置（当 HTTP Headers 未传递凭证时使用）
type CredentialsConfig struct {
	// 腾讯云 SecretId
	SecretId string `yaml:"secret_id"`
	// 腾讯云 SecretKey
	SecretKey string `yaml:"secret_key"`
	// 环境（可选值: test / online）
	Env string `yaml:"env"`
}

// Config MCP Server 完整配置
type Config struct {
	Server      ServerConfig      `yaml:"server"`
	API         APIConfig         `yaml:"api"`
	Schema      SchemaConfig      `yaml:"schema"`
	Credentials CredentialsConfig `yaml:"credentials"`
}

// ServerConfig 服务配置
type ServerConfig struct {
	// MCP Server 监听端口
	Port string `yaml:"port"`
	// MCP Server 名称
	Name string `yaml:"name"`
	// MCP Server 版本
	Version string `yaml:"version"`
	// 是否开启 debug 模式，开启后会打印请求参数、响应内容等详细日志
	Debug bool `yaml:"debug"`
}

// APIConfig API 相关配置
type APIConfig struct {
	// 腾讯云 API 服务名
	Service string `yaml:"service"`
	// API 版本号
	APIVersion string `yaml:"api_version"`
	// Endpoint 配置（按环境区分）
	Endpoints map[string]EndpointConfig `yaml:"endpoints"`
	// 需要加载的 API 接口白名单，只有在此列表中的接口才会被注册为 MCP 工具
	// 如果列表为空，则加载所有接口
	LoadingAPIs []string `yaml:"loading_apis"`
}

// EndpointConfig 单个环境的 Endpoint 配置
type EndpointConfig struct {
	// 默认 Endpoint
	Default string `yaml:"default"`
	// 特殊接口的 Endpoint 映射（接口名 -> Endpoint）
	Custom map[string]string `yaml:"custom"`
}

// SchemaConfig Schema 描述长度限制配置
type SchemaConfig struct {
	// 工具列表中精简描述的最大长度
	DescMaxLenShort int `yaml:"desc_max_len_short"`
	// 参数描述的最大长度
	DescMaxLenMedium int `yaml:"desc_max_len_medium"`
	// 接口详情描述的最大长度
	DescMaxLenLong int `yaml:"desc_max_len_long"`
	// 参数详细说明最大递归深度
	SchemaMaxDetailDepth int `yaml:"schema_max_detail_depth"`
}

// LoadFromFile 从 YAML 配置文件中加载配置
func LoadFromFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 设置默认值
	cfg.setDefaults()

	// 初始化全局 debug 标识
	globalDebug = cfg.Server.Debug

	return cfg, nil
}

// setDefaults 为未设置的配置项填充默认值
func (c *Config) setDefaults() {
	// Server 默认值
	if c.Server.Port == "" {
		c.Server.Port = "8080"
	}
	if c.Server.Name == "" {
		c.Server.Name = "腾讯电子签 ESS MCP Server"
	}
	if c.Server.Version == "" {
		c.Server.Version = "1.0.0"
	}

	// API 默认值
	if c.API.Service == "" {
		c.API.Service = "ess"
	}
	if c.API.APIVersion == "" {
		c.API.APIVersion = "2020-11-11"
	}

	// Schema 默认值
	if c.Schema.DescMaxLenShort == 0 {
		c.Schema.DescMaxLenShort = 300
	}
	if c.Schema.DescMaxLenMedium == 0 {
		c.Schema.DescMaxLenMedium = 150
	}
	if c.Schema.DescMaxLenLong == 0 {
		c.Schema.DescMaxLenLong = 300
	}
	if c.Schema.SchemaMaxDetailDepth == 0 {
		c.Schema.SchemaMaxDetailDepth = 4
	}
}

// GetEndpoint 根据环境和接口名称返回对应的 Endpoint
// 如果未配置对应环境的 Endpoint，返回错误
func (c *Config) GetEndpoint(env string, actionName string) (string, error) {
	epCfg, ok := c.API.Endpoints[env]
	if !ok {
		return "", fmt.Errorf("未配置环境 [%s] 的 Endpoint，请检查配置文件中的 api.endpoints 配置项", env)
	}

	// 先查特殊接口映射
	if ep, exists := epCfg.Custom[actionName]; exists {
		return ep, nil
	}

	if epCfg.Default == "" {
		return "", fmt.Errorf("环境 [%s] 未配置默认 Endpoint，请检查配置文件中的 api.endpoints.%s.default 配置项", env, env)
	}

	return epCfg.Default, nil
}
