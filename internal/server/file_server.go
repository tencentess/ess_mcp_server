package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ess_mcp_server/internal/server/custom"
)

// 文件存储的最大大小限制 50MB
const maxUploadSize = 50 * 1024 * 1024

// 文件缓存过期时间 30 分钟
const fileCacheTTL = 30 * time.Minute

// uploadDir 文件上传的磁盘临时目录
var uploadDir string

func init() {
	uploadDir = filepath.Join(os.TempDir(), "ess_mcp_uploads")
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Printf("[FileServer] 警告: 创建上传临时目录失败: %v", err)
	}
}

// FileServer 文件上传服务
type FileServer struct{}

// NewFileServer 创建文件服务实例
func NewFileServer() *FileServer {
	fs := &FileServer{}
	// 启动后台清理过期文件的协程
	go fs.cleanupLoop()
	return fs
}

// cleanupLoop 定期清理过期的缓存文件
func (fs *FileServer) cleanupLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		fs.cleanup()
	}
}

// cleanup 清理过期文件，委托给 custom.CleanupExpiredFiles
func (fs *FileServer) cleanup() {
	custom.CleanupExpiredFiles(fileCacheTTL)
}

// generateFileId 生成随机文件ID
func generateFileId() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// HandleUpload 处理文件上传请求
// POST /upload
// Content-Type: multipart/form-data
// 字段名: file
// 返回: {"fileId": "xxx", "fileName": "xxx", "fileSize": 123}
func (fs *FileServer) HandleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "仅支持 POST 方法", http.StatusMethodNotAllowed)
		return
	}

	// 限制请求体大小
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadSize+1024)

	if err := r.ParseMultipartForm(maxUploadSize); err != nil {
		http.Error(w, fmt.Sprintf("解析上传文件失败: %v", err), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, fmt.Sprintf("获取上传文件失败: %v", err), http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 校验文件扩展名
	ext := strings.ToLower(filepath.Ext(header.Filename))
	allowedExts := map[string]bool{
		".pdf": true, ".doc": true, ".docx": true,
		".jpg": true, ".jpeg": true, ".png": true,
		".xls": true, ".xlsx": true, ".html": true,
		".bmp": true, ".txt": true,
	}
	if ext == "" || !allowedExts[ext] {
		http.Error(w, fmt.Sprintf("不支持的文件类型 '%s'，支持: pdf, doc, docx, jpg, jpeg, png, xls, xlsx, html, bmp, txt", ext), http.StatusBadRequest)
		return
	}

	// 生成文件ID
	fileId := generateFileId()

	// 将文件写入磁盘临时目录
	diskPath := filepath.Join(uploadDir, fileId+ext)
	outFile, err := os.Create(diskPath)
	if err != nil {
		http.Error(w, fmt.Sprintf("创建临时文件失败: %v", err), http.StatusInternalServerError)
		return
	}

	written, err := io.Copy(outFile, file)
	outFile.Close()
	if err != nil {
		os.Remove(diskPath)
		http.Error(w, fmt.Sprintf("写入文件失败: %v", err), http.StatusInternalServerError)
		return
	}

	if written > maxUploadSize {
		os.Remove(diskPath)
		http.Error(w, fmt.Sprintf("文件大小超过限制 (最大 %dMB)", maxUploadSize/1024/1024), http.StatusBadRequest)
		return
	}

	// 将文件信息记录到全局 map
	custom.StoreFile(fileId, diskPath, header.Filename)

	log.Printf("[FileServer] 文件上传成功: fileId=%s, fileName=%s, size=%d, path=%s", fileId, header.Filename, written, diskPath)

	// 返回结果
	resp := map[string]interface{}{
		"fileId":   fileId,
		"fileName": header.Filename,
		"fileSize": written,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}