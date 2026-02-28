package custom

import (
	"ess_mcp_server/internal/parser"
)

// ActionCustomizer 接口定制化处理器
// 每个需要定制的接口实现此接口，放在 custom 目录下的独立文件中
type ActionCustomizer interface {
	// ActionName 返回要定制的接口名称
	ActionName() string

	// CustomizeSchema 定制 Schema 输出
	// 在 get_tool_schema 返回给大模型之前，对参数定义进行修改
	// 例如：将 FileBody 替换为 FileUrl / FilePath
	CustomizeSchema(params []parser.ParamDef) []parser.ParamDef

	// PreprocessParams 在调用 API 之前对参数进行预处理
	// 例如：将 url 下载为文件并转为 base64，或将本地路径读取并转为 base64
	// 返回处理后的参数和可能的错误
	PreprocessParams(params map[string]interface{}) (map[string]interface{}, error)
}

// 全局定制化处理器注册表
var customizerRegistry = make(map[string]ActionCustomizer)

// 描述追加注册表：用于在工具描述末尾追加额外的说明信息
var descriptionSuffixRegistry = make(map[string]string)

// Register 注册一个接口定制化处理器
func Register(c ActionCustomizer) {
	customizerRegistry[c.ActionName()] = c
}

// Get 获取指定接口的定制化处理器，不存在返回 nil
func Get(actionName string) ActionCustomizer {
	return customizerRegistry[actionName]
}

// RegisterDescriptionSuffix 注册一个接口的描述追加内容
// 该内容会追加到工具描述的末尾，用于补充说明
func RegisterDescriptionSuffix(actionName string, suffix string) {
	descriptionSuffixRegistry[actionName] = suffix
}

// GetDescriptionSuffix 获取指定接口的描述追加内容，不存在返回空字符串
func GetDescriptionSuffix(actionName string) string {
	return descriptionSuffixRegistry[actionName]
}

func init() {
	// 在此处注册所有定制化处理器
	// 每新增一个定制接口，只需在对应文件的 init() 中调用 Register 即可
	Register(&UploadFilesCustomizer{})
}
