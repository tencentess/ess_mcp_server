package custom

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ess_mcp_server/internal/config"
	"ess_mcp_server/internal/parser"
)

// 允许上传的文件扩展名白名单
var allowedFileExtensions = map[string]bool{
	".pdf":  true,
	".doc":  true,
	".docx": true,
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".xls":  true,
	".xlsx": true,
	".html": true,
}

// UploadFilesCustomizer 对 UploadFiles 接口的定制化处理
// 将 FileBody（base64）替换为 FileUrl / FilePath，MCP Server 自动完成文件读取和编码
type UploadFilesCustomizer struct{}

func (u *UploadFilesCustomizer) ActionName() string {
	return "UploadFiles"
}

// CustomizeSchema 修改 UploadFiles 的 Schema：
// 将 FileInfos[].FileBody 移除，替换为 FileUrl 和 FilePath 两个字段
func (u *UploadFilesCustomizer) CustomizeSchema(params []parser.ParamDef) []parser.ParamDef {
	for i, p := range params {
		if p.Name == "FileInfos" && p.Items != nil {
			params[i].Items = customizeUploadFileItems(p.Items)
		}
	}
	return params
}

// customizeUploadFileItems 修改 UploadFile 结构的参数定义
func customizeUploadFileItems(items *parser.ParamDef) *parser.ParamDef {
	newProps := make([]parser.ParamDef, 0, len(items.Properties)+2)

	for _, prop := range items.Properties {
		// 移除 FileBody，替换为 FileUrl 和 FilePath
		if prop.Name == "FileBody" {
			newProps = append(newProps, parser.ParamDef{
				Name:        "FileUrl",
				Type:        "string",
				Description: "文件下载URL地址。提供此参数后，MCP Server 会自动下载文件并转换为 Base64 上传。与 FilePath 二选一，优先使用 FileUrl。",
				Required:    false,
				Example:     "https://example.com/contract.pdf",
			})
			newProps = append(newProps, parser.ParamDef{
				Name:        "FilePath",
				Type:        "string",
				Description: "本地文件路径。提供此参数后，MCP Server 会自动读取文件并转换为 Base64 上传。支持的文件类型: .pdf/.doc/.docx/.jpg/.png/.xls/.xlsx/.html。与 FileUrl 二选一。",
				Required:    false,
				Example:     "C:/Documents/contract.pdf",
			})
			continue
		}
		newProps = append(newProps, prop)
	}

	items.Properties = newProps
	return items
}

// PreprocessParams 在调用 UploadFiles API 前预处理参数：
// 将 FileUrl/FilePath 转换为 FileBody（base64）
func (u *UploadFilesCustomizer) PreprocessParams(params map[string]interface{}) (map[string]interface{}, error) {
	fileInfos, ok := params["FileInfos"]
	if !ok {
		return params, nil
	}

	fileInfoArr, ok := fileInfos.([]interface{})
	if !ok {
		return params, nil
	}

	for i, item := range fileInfoArr {
		fileInfo, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		var fileBytes []byte
		var fromUrl bool
		var err error

		// 优先处理 FileUrl
		if fileUrl, ok := fileInfo["FileUrl"].(string); ok && fileUrl != "" {
			fileBytes, err = downloadFile(fileUrl)
			if err != nil {
				return nil, fmt.Errorf("FileInfos[%d]: 下载文件失败 (url=%s): %w", i, fileUrl, err)
			}
			fromUrl = true
			// 移除自定义字段
			delete(fileInfo, "FileUrl")
			config.Debug("[UploadFiles定制] FileInfos[%d]: 从URL下载文件成功，大小: %d 字节", i, len(fileBytes))
		} else if filePath, ok := fileInfo["FilePath"].(string); ok && filePath != "" {
			// 处理 FilePath：先检查文件是否存在，不存在则提示用户提供 URL
			if _, statErr := os.Stat(filePath); statErr != nil && os.IsNotExist(statErr) {
				return nil, fmt.Errorf("FileInfos[%d]: 本地文件不存在 (path=%s)，MCP Server 无法访问该路径的文件，请提供文件的下载URL（使用 FileUrl 参数）", i, filePath)
			}
			fileBytes, err = readLocalFile(filePath)
			if err != nil {
				return nil, fmt.Errorf("FileInfos[%d]: 读取本地文件失败 (path=%s): %w", i, filePath, err)
			}
			fromUrl = false
			// 移除自定义字段
			delete(fileInfo, "FilePath")
			config.Debug("[UploadFiles定制] FileInfos[%d]: 读取本地文件成功，大小: %d 字节, 路径: %s", i, len(fileBytes), filePath)
		}

		// 如果成功获取到文件内容，转为 base64 赋值给 FileBody
		if fileBytes != nil {
			fileInfo["FileBody"] = base64.StdEncoding.EncodeToString(fileBytes)
			config.Debug("[UploadFiles定制] FileInfos[%d]: Base64编码完成，来源: url=%v", i, fromUrl)
		}

		fileInfoArr[i] = fileInfo
	}

	params["FileInfos"] = fileInfoArr
	return params, nil
}

// downloadFile 从 URL 下载文件内容
func downloadFile(url string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("HTTP请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP状态码异常: %d", resp.StatusCode)
	}

	// 限制最大下载大小为 50MB
	const maxFileSize = 50 * 1024 * 1024
	limitReader := io.LimitReader(resp.Body, maxFileSize+1)

	data, err := io.ReadAll(limitReader)
	if err != nil {
		return nil, fmt.Errorf("读取响应内容失败: %w", err)
	}

	if len(data) > maxFileSize {
		return nil, fmt.Errorf("文件大小超过限制 (最大 %dMB)", maxFileSize/1024/1024)
	}

	return data, nil
}

// readLocalFile 读取本地文件，并校验文件扩展名
func readLocalFile(filePath string) ([]byte, error) {
	// 校验文件扩展名
	ext := strings.ToLower(filepath.Ext(filePath))
	if ext == "" {
		return nil, fmt.Errorf("文件缺少扩展名，无法判断文件类型")
	}
	if !allowedFileExtensions[ext] {
		return nil, fmt.Errorf("不支持的文件类型 '%s'，支持的类型: %s", ext, getAllowedExtensions())
	}

	// 检查文件是否存在
	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("文件不存在: %s", filePath)
		}
		return nil, fmt.Errorf("无法访问文件: %w", err)
	}

	// 限制文件大小 50MB
	const maxFileSize = 50 * 1024 * 1024
	if info.Size() > maxFileSize {
		return nil, fmt.Errorf("文件大小 (%d 字节) 超过限制 (最大 %dMB)", info.Size(), maxFileSize/1024/1024)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("读取文件失败: %w", err)
	}

	return data, nil
}

// getAllowedExtensions 返回所有允许的文件扩展名（用于错误提示）
func getAllowedExtensions() string {
	exts := make([]string, 0, len(allowedFileExtensions))
	for ext := range allowedFileExtensions {
		exts = append(exts, ext)
	}
	return strings.Join(exts, ", ")
}
