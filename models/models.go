package models

import (
	"sync"
)

// CoverStatus 表示封面状态
type CoverStatus string

const (
	CoverStatusNotSupported    CoverStatus = "不支持"
	CoverStatusBuiltIn         CoverStatus = "内置封面"
	CoverStatusWaitingDownload CoverStatus = "等待下载封面"
	CoverStatusDownloading     CoverStatus = "下载封面中"
	CoverStatusDownloaded      CoverStatus = "下载完成"
)

// ConvertStatus 表示转码状态
type ConvertStatus string

const (
	ConvertStatusWaiting    ConvertStatus = "等待转码"
	ConvertStatusConverting ConvertStatus = "转码中"
	ConvertStatusConverted  ConvertStatus = "转码完成"
	ConvertStatusMerged     ConvertStatus = "合并完成"
	ConvertStatusError      ConvertStatus = "错误"
	ConvertStatusSkipped    ConvertStatus = "已跳过"
)

// FileInfo 表示一个待处理的文件信息
type FileInfo struct {
	ID            int           `json:"id"`
	Path          string        `json:"path"`
	SongName      string        `json:"songName"`
	Format        string        `json:"format"`
	Size          int64         `json:"size"`
	CoverStatus   CoverStatus   `json:"coverStatus"`
	ConvertStatus ConvertStatus `json:"convertStatus"`
	Logs          []string      `json:"logs"`
	mu            sync.Mutex
}

// AddLog 添加日志到文件日志列表
func (fi *FileInfo) AddLog(log string) {
	fi.mu.Lock()
	defer fi.mu.Unlock()
	fi.Logs = append(fi.Logs, log)
}

// GetLogs 获取文件日志列表
func (fi *FileInfo) GetLogs() []string {
	fi.mu.Lock()
	defer fi.mu.Unlock()
	logs := make([]string, len(fi.Logs))
	copy(logs, fi.Logs)
	return logs
}

// AppConfig 表示应用程序配置
type AppConfig struct {
	AutoDownloadCover      bool `json:"autoDownloadCover"`
	MaxDownloadConcurrency int  `json:"maxDownloadConcurrency"`
	MaxConvertConcurrency  int  `json:"maxConvertConcurrency"`
}

// DefaultConfig 返回默认配置
func DefaultConfig() *AppConfig {
	return &AppConfig{
		AutoDownloadCover:  true,
		MaxDownloadConcurrency: 3,
		MaxConvertConcurrency:  10,
	}
}

// ConvertResult 表示转换结果统计
type ConvertResult struct {
	SuccessCount int `json:"successCount"`
	FailedCount  int `json:"failedCount"`
	SkippedCount int `json:"skippedCount"`
}
