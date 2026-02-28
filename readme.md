
# 腾讯电子签 ESS MCP Server

基于 MCP (Model Context Protocol)的腾讯电子签 API 服务网关，将腾讯电子签 API 接口注册为 MCP 工具，供大模型调用。

---

## 编译

### 环境要求

- Go 1.23.0 或更高版本

### 编译步骤

```bash
git clone https://git.woa.com/shikiliu/ess_mcp_server
cd ess_mcp_server
go mod tidy
go build
```
编译完成后会在当前目录生成 `ess_mcp_server` 可执行文件

---

## 配置

程序启动时会自动读取 **可执行文件同目录下** 的 `config.yaml`和 `yaml` 文件夹下的内容。

### 部署结构

```
├── ess_mcp_server       # 可执行文件
├── config.yaml          # 配置文件
└── yaml/                # Swagger API 定义文件目录
    ├── ess_1.yaml
    └── ess_2.yaml
```

### 配置文件说明 (`config.yaml`)

```yaml
server:
  # MCP Server 监听端口（默认 8080）
  port: "8080"
  # MCP Server 名称
  name: "腾讯电子签 ESS MCP Server"
  # MCP Server 版本
  version: "1.0.0"
  # 是否开启 debug 模式（开启后会打印请求参数、响应内容等详细日志）
  debug: true

schema:
  # 工具列表中精简描述的最大长度（默认 300）
  desc_max_len_short: 300
  # 接口详情描述的最大长度（默认 400）
  desc_max_len_long: 400
  # 每个参数描述的最大长度（默认 150）
  desc_max_len_medium: 150
  # 参数详细说明最大递归深度（默认 4，太深会导致 mcp client too large 报错）
  schema_max_detail_depth: 4
  
#（可以通过 mcp client HTTP Headers 中传递的覆盖）
credentials:
  # 腾讯云 SecretId
  secret_id: "你的 SecretId"
  # 腾讯云 SecretKey
  secret_key: "你的 SecretKey"
  # 环境（可选值: test / online）
  env: "test"

api:
  # 腾讯云 API 服务名（ess 或 essbasic）
  service: "ess"
  # 腾讯云 API 版本号
  api_version: "2020-11-11"
  # Endpoint 配置（按环境区分）
  endpoints:
    test:
      default: "ess.test.ess.tencent.cn"
      custom:
        UploadFiles: "file.test.ess.tencent.cn"
    online:
      default: "ess.tencentcloudapi.com"
      custom:
        UploadFiles: "file.ess.tencent.cn"

  # API 接口白名单（只加载列表中的接口，留空则加载全部）
  # 建议按需配置，API 太多会导致 token 消耗过快
  loading_apis:
    - DescribeFlowTemplates
    - CreateFlowByFiles
    - DescribeFlowInfo
    # ... 按需添加
```

### 关键配置说明

| 配置项 | 说明 |
|---|---|
| `server.port` | 服务监听端口，默认 `8080` |
| `server.debug` | 开启后会在 `./log/` 目录下输出详细的请求和响应日志 |
| `credentials` | 默认的腾讯云凭证，当 MCP Client 未通过 HTTP Headers 传递凭证时使用 |
| `api.service` | 决定加载 `yaml/` 目录下哪些 Swagger 文件（如 `ess` 则加载 `ess_*.yaml`） |
| `api.loading_apis` | API 白名单，建议只配置需要的接口，避免注册过多工具导致 token 超限 |
| `schema.*` | 控制描述文本的截断长度和参数递归深度，用于平衡准确度与 token 消耗 |

---

## 使用

### 启动服务

```bash
./ess_mcp_server
```


### MCP Client 接入

MCP Client 连接地址：

```
{
	"mcpServers":{
		"ess":{
			"url":"http://你部署服务的地址:8080/mcp"
		}
	}
}
```



#### MCP Client用自己的凭证

```json
{
	"mcpServers":{
		"ess":{
			"url":"http://你部署服务的地址:8080/mcp",
			"headers":{
				"X-Secret-Id":"AK******S7BIlPZPZwx",
				"X-Secret-Key":"SK********g0j",
				"X-Env":"test"
			}
		}
	}
}


```
| Header | 说明 |
|---|---|
| `X-Secret-Id` | 腾讯云 SecretId |
| `X-Secret-Key` | 腾讯云 SecretKey |
| `X-Env` | 环境，可选值：`test` / `online` |

如果 HTTP Headers 中未传递凭证，则自动使用 `config.yaml` 中 `credentials` 部分的配置。

### 日志

日志文件位于可执行文件同目录的 `./log/` 目录下，文件名为 `<主机名>.log`，自动轮转（单文件最大 500MB，保留 10 个备份）。
