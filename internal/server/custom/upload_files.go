package custom

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"time"

	"ess_mcp_server/internal/config"
	"ess_mcp_server/internal/parser"
)

// UploadFilesCustomizer 对 UploadFiles 接口的定制化处理
// 将 FileBody（base64）替换为 FileUrl / FileUploadId，MCP Server 自动完成文件读取和编码
type UploadFilesCustomizer struct {
	// usedFileIds 暂存本次请求中已使用的 FileUploadId 列表，供 PostprocessParams 清理
	usedFileIds []string
}

func (u *UploadFilesCustomizer) ActionName() string {
	return "UploadFiles"
}

// CustomizeSchema 修改 UploadFiles 的 Schema：
// 将 FileInfos[].FileBody 移除，替换为 FileUrl 和 FileUploadId 两个字段
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
		// 移除 FileBody，替换为 FileUrl 和 FileUploadId
		if prop.Name == "FileBody" {
			newProps = append(newProps, parser.ParamDef{
				Name: "FileUrl",
				Type: "string",
				Description: "文件下载URL地址，与FileUploadId二选一，如果同时提供则优先使用FileUploadId。" +
					"测试可用：https://qcloudimg.tencent-cloud.cn/raw/4221137b4fad7ac4b60f8ab96961b81a.pdf ",
				Required:     false,
				Example:      "https://qcloudimg.tencent-cloud.cn/raw/4221137b4fad7ac4b60f8ab96961b81a.pdf",
				SkipTruncate: true,
			})
			newProps = append(newProps, parser.ParamDef{
				Name: "FileUploadId",
				Type: "string",
				Description: fmt.Sprintf(
					"通过 POST /upload 端点上传文件后返回的 fileId，与FileUrl二选一，如果同时提供则优先使用FileUploadId。"+
						"使用方法：先通过 POST %s/upload 上传本地文件（multipart/form-data，字段名: file），然后将返回的 fileId 填入此参数。",
					GetBaseUrl()),
				Required:     false,
				SkipTruncate: true,
			})
			continue
		}
		newProps = append(newProps, prop)
	}

	items.Properties = newProps
	return items
}

// PreprocessParams 在调用 UploadFiles API 前预处理参数：
// 将 FileUrl/FileUploadId 转换为 FileBody（base64）
func (u *UploadFilesCustomizer) PreprocessParams(params map[string]interface{}) (map[string]interface{}, error) {
	// 重置已使用的 fileId 列表
	u.usedFileIds = nil

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

		// 优先处理 FileUploadId，如果同时提供了 FileUploadId 和 FileUrl，则使用 FileUploadId
		if fileUploadId, ok := fileInfo["FileUploadId"].(string); ok && fileUploadId != "" {
			// 通过全局 FileStore 获取已上传的文件内容
			data, fileName, err := GetFileBytes(fileUploadId)
			if err != nil {
				return nil, fmt.Errorf("FileInfos[%d]: 通过 FileUploadId 获取文件失败: %w", i, err)
			}
			fileBytes = data
			// 记录已使用的 fileId，供后续清理
			u.usedFileIds = append(u.usedFileIds, fileUploadId)
			// 移除自定义字段
			delete(fileInfo, "FileUploadId")
			delete(fileInfo, "FileUrl") // 同时移除 FileUrl（如果存在）
			config.Debug("[UploadFiles定制] FileInfos[%d]: 通过 FileUploadId 获取文件成功，文件名: %s，大小: %d 字节", i, fileName, len(fileBytes))
		} else if fileUrl, ok := fileInfo["FileUrl"].(string); ok && fileUrl != "" {
			data, err := downloadFile(fileUrl)
			if err != nil {
				return nil, fmt.Errorf("FileInfos[%d]: 下载文件失败 (url=%s): %w", i, fileUrl, err)
			}
			fileBytes = data
			// 移除自定义字段
			delete(fileInfo, "FileUrl")
			config.Debug("[UploadFiles定制] FileInfos[%d]: 从URL下载文件成功，大小: %d 字节", i, len(fileBytes))
		}

		// 如果成功获取到文件内容，转为 base64 赋值给 FileBody
		if fileBytes != nil {
			fileInfo["FileBody"] = base64.StdEncoding.EncodeToString(fileBytes)
		}

		fileInfoArr[i] = fileInfo
	}

	params["FileInfos"] = fileInfoArr
	return params, nil
}

// PostprocessParams 在 UploadFiles API 调用成功后清理本地临时文件
func (u *UploadFilesCustomizer) PostprocessParams(params map[string]interface{}) error {
	for _, fileId := range u.usedFileIds {
		RemoveFile(fileId)
		config.Debug("[UploadFiles定制] API 调用成功，已清理本地文件: fileId=%s", fileId)
	}
	// 清空已使用列表
	u.usedFileIds = nil
	return nil
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