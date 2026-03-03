package custom

import (
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

// fileRecord 文件记录，保存文件路径和元信息
type fileRecord struct {
	FilePath  string    // 文件在磁盘上的路径
	FileName  string    // 文件原始名称
	CreatedAt time.Time // 文件上传时间
}

var (
	// fileMap 全局文件 ID -> 文件记录的映射
	fileMap = make(map[string]*fileRecord)
	// fileMu 保护 fileMap 的读写锁
	fileMu sync.RWMutex
	// baseUrl 文件服务的基础地址
	baseUrl string
)

// InitFileStore 初始化全局文件存储，设置文件服务基础地址
func InitFileStore(serverBaseUrl string) {
	fileMu.Lock()
	defer fileMu.Unlock()
	baseUrl = serverBaseUrl
	fileMap = make(map[string]*fileRecord)
	log.Printf("[FileStore] 初始化完成，baseUrl=%s", serverBaseUrl)
}

// StoreFile 向全局 map 中记录文件信息
func StoreFile(fileId, filePath, fileName string) {
	fileMu.Lock()
	defer fileMu.Unlock()
	fileMap[fileId] = &fileRecord{
		FilePath:  filePath,
		FileName:  fileName,
		CreatedAt: time.Now(),
	}
}

// GetFileBytes 根据 fileId 从全局 map 获取文件路径，读取磁盘文件内容并返回
// 返回值：文件内容、文件名、错误
func GetFileBytes(fileId string) ([]byte, string, error) {
	fileMu.RLock()
	record, ok := fileMap[fileId]
	fileMu.RUnlock()

	if !ok {
		return nil, "", fmt.Errorf("文件不存在或已过期 (fileId=%s)", fileId)
	}

	data, err := os.ReadFile(record.FilePath)
	if err != nil {
		return nil, "", fmt.Errorf("读取磁盘文件失败 (fileId=%s, path=%s): %w", fileId, record.FilePath, err)
	}

	return data, record.FileName, nil
}

// GetBaseUrl 返回文件服务的基础 URL
func GetBaseUrl() string {
	fileMu.RLock()
	defer fileMu.RUnlock()
	return baseUrl
}

// RemoveFile 从全局 map 中删除文件记录，并删除磁盘上的临时文件
// 删除磁盘文件失败时仅记录警告日志，不返回错误
func RemoveFile(fileId string) {
	fileMu.Lock()
	record, ok := fileMap[fileId]
	if ok {
		delete(fileMap, fileId)
	}
	fileMu.Unlock()

	if !ok {
		return
	}

	if err := os.Remove(record.FilePath); err != nil && !os.IsNotExist(err) {
		log.Printf("[FileStore] 警告: 删除磁盘文件失败 (fileId=%s, path=%s): %v", fileId, record.FilePath, err)
	} else {
		log.Printf("[FileStore] 已清理文件: fileId=%s, fileName=%s", fileId, record.FileName)
	}
}

// CleanupExpiredFiles 清理超过 TTL 的过期文件记录和磁盘文件
func CleanupExpiredFiles(ttl time.Duration) {
	fileMu.Lock()
	now := time.Now()
	var expiredIds []string
	for id, record := range fileMap {
		if now.Sub(record.CreatedAt) > ttl {
			expiredIds = append(expiredIds, id)
		}
	}
	// 先从 map 中删除，收集需要清理的文件路径
	type cleanupItem struct {
		id       string
		filePath string
		fileName string
	}
	items := make([]cleanupItem, 0, len(expiredIds))
	for _, id := range expiredIds {
		record := fileMap[id]
		items = append(items, cleanupItem{id: id, filePath: record.FilePath, fileName: record.FileName})
		delete(fileMap, id)
	}
	fileMu.Unlock()

	// 在锁外删除磁盘文件
	for _, item := range items {
		if err := os.Remove(item.filePath); err != nil && !os.IsNotExist(err) {
			log.Printf("[FileStore] 警告: 清理过期文件失败 (fileId=%s, path=%s): %v", item.id, item.filePath, err)
		} else {
			log.Printf("[FileStore] 清理过期文件: fileId=%s, fileName=%s", item.id, item.fileName)
		}
	}
}
