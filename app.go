package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lemonlemon-work/ncm-converter/converter"
	"github.com/lemonlemon-work/ncm-converter/models"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx           context.Context
	config        *models.AppConfig
	converter     *converter.Converter
	fileList      []*models.FileInfo
	fileListMu    sync.Mutex
	nextID        int
	logEntries    []string
	logMu         sync.Mutex
	isProcessing  bool
	stopChan      chan struct{}
	selectedFile  *models.FileInfo
	listenersMu   sync.Mutex
}

func NewApp() *App {
	config := models.DefaultConfig()
	return &App{
		config:    config,
		converter: converter.NewConverter(config),
		nextID:    1,
		stopChan:  make(chan struct{}),
	}
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
}

func (a *App) GetConfig() *models.AppConfig {
	return a.config
}

func (a *App) SaveConfig(config *models.AppConfig) {
	if config.MaxDownloadConcurrency <= 0 {
		config.MaxDownloadConcurrency = 3
	}
	if config.MaxConvertConcurrency <= 0 {
		config.MaxConvertConcurrency = 10
	}
	a.config = config
	a.converter = converter.NewConverter(config)
	runtime.EventsEmit(a.ctx, "config-saved")
}

func (a *App) GetFileList() []*models.FileInfo {
	a.fileListMu.Lock()
	defer a.fileListMu.Unlock()
	result := make([]*models.FileInfo, len(a.fileList))
	for i, f := range a.fileList {
		result[i] = &models.FileInfo{
			ID:            f.ID,
			Path:          f.Path,
			SongName:      f.SongName,
			Format:        f.Format,
			Size:          f.Size,
			CoverStatus:   f.CoverStatus,
			ConvertStatus: f.ConvertStatus,
			Logs:          make([]string, len(f.Logs)),
		}
		copy(result[i].Logs, f.Logs)
	}
	return result
}

func (a *App) AddDirectory() ([]string, error) {
	dir, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择目录",
	})
	if err != nil {
		return nil, err
	}
	if dir == "" {
		return nil, nil
	}
	return a.addFilesFromDirectory(dir)
}

func (a *App) AddFiles() ([]string, error) {
	files, err := runtime.OpenMultipleFilesDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "选择NCM文件",
		Filters: []runtime.FileFilter{
			{
				DisplayName: "NCM文件 (*.ncm)",
				Pattern:     "*.ncm",
			},
		},
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}
	return a.addFilesBatch(files)
}

func (a *App) AddAppDirectory() ([]string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, err
	}
	appDir := filepath.Dir(exePath)
	return a.addFilesFromDirectory(appDir)
}

func (a *App) addFilesFromDirectory(dir string) ([]string, error) {
	ncmFiles, err := converter.FindNCMFiles(dir)
	if err != nil {
		return nil, err
	}

	if len(ncmFiles) == 0 {
		return nil, nil
	}

	return a.addFilesBatch(ncmFiles)
}

func (a *App) addFilesBatch(filePaths []string) ([]string, error) {
	a.fileListMu.Lock()
	existingPaths := make(map[string]bool)
	for _, f := range a.fileList {
		existingPaths[f.Path] = true
	}
	a.fileListMu.Unlock()

	var newFilePaths []string
	for _, filePath := range filePaths {
		if !strings.EqualFold(filepath.Ext(filePath), ".ncm") {
			continue
		}
		if !existingPaths[filePath] {
			newFilePaths = append(newFilePaths, filePath)
			existingPaths[filePath] = true
		}
	}

	if len(newFilePaths) == 0 {
		return nil, nil
	}

	maxConcurrency := 5
	semaphore := make(chan struct{}, maxConcurrency)
	var wg sync.WaitGroup
	var newFilesMu sync.Mutex
	var newFiles []*models.FileInfo

	a.fileListMu.Lock()
	currentID := a.nextID
	a.fileListMu.Unlock()

	for i, filePath := range newFilePaths {
		wg.Add(1)
		go func(fp string, idx int, startID int) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			ncmInfo, err := converter.GetNCMFileInfo(fp)
			if err != nil {
				size, _ := converter.GetFileSize(fp)
				fileInfo := &models.FileInfo{
					ID:            startID + idx,
					Path:          fp,
					SongName:      converter.GetSongName(fp),
					Format:        "",
					Size:          size,
					CoverStatus:   models.CoverStatusNotSupported,
					ConvertStatus: models.ConvertStatusWaiting,
					Logs:          []string{fmt.Sprintf("解析文件信息失败: %v", err)},
				}
				newFilesMu.Lock()
				newFiles = append(newFiles, fileInfo)
				newFilesMu.Unlock()
				return
			}

			size, err := converter.GetFileSize(fp)
			if err != nil {
				size = 0
			}

			fileInfo := &models.FileInfo{
				ID:            startID + idx,
				Path:          fp,
				SongName:      ncmInfo.SongName,
				Format:        ncmInfo.Format,
				Size:          size,
				CoverStatus:   ncmInfo.CoverStatus,
				ConvertStatus: models.ConvertStatusWaiting,
				Logs:          []string{},
			}

			newFilesMu.Lock()
			newFiles = append(newFiles, fileInfo)
			newFilesMu.Unlock()
		}(filePath, i, currentID)
	}

	wg.Wait()

	if len(newFiles) > 0 {
		a.fileListMu.Lock()
		existingPaths = make(map[string]bool)
		for _, f := range a.fileList {
			existingPaths[f.Path] = true
		}

		var filesToAdd []*models.FileInfo
		for _, f := range newFiles {
			if !existingPaths[f.Path] {
				filesToAdd = append(filesToAdd, f)
				existingPaths[f.Path] = true
			}
		}

		if len(filesToAdd) > 0 {
			a.fileList = append(a.fileList, filesToAdd...)
			maxID := a.nextID
			for _, f := range filesToAdd {
				if f.ID >= maxID {
					maxID = f.ID + 1
				}
			}
			a.nextID = maxID
		}
		a.fileListMu.Unlock()

		a.addLog(fmt.Sprintf("添加了 %d 个NCM文件", len(filesToAdd)))
		runtime.EventsEmit(a.ctx, "file-list-updated")

		addedPaths := make([]string, len(filesToAdd))
		for i, f := range filesToAdd {
			addedPaths[i] = f.Path
		}
		return addedPaths, nil
	}

	return nil, nil
}

func (a *App) ClearFileList() {
	a.fileListMu.Lock()
	a.fileList = []*models.FileInfo{}
	a.nextID = 1
	a.selectedFile = nil
	a.fileListMu.Unlock()
	a.addLog("已清空文件列表")
	runtime.EventsEmit(a.ctx, "file-list-updated")
}

func (a *App) StartConversion() error {
	a.fileListMu.Lock()
	if len(a.fileList) == 0 {
		a.fileListMu.Unlock()
		return fmt.Errorf("请先添加NCM文件")
	}
	a.fileListMu.Unlock()

	if a.isProcessing {
		return fmt.Errorf("正在处理中")
	}

	a.isProcessing = true
	a.stopChan = make(chan struct{})

	go func() {
		a.fileListMu.Lock()
		filesToProcess := make([]*models.FileInfo, len(a.fileList))
		copy(filesToProcess, a.fileList)
		a.fileListMu.Unlock()

		a.addLog(fmt.Sprintf("开始处理 %d 个NCM文件...", len(filesToProcess)))

		result := a.converter.ProcessFilesConcurrently(
			filesToProcess,
			func(log string) {
				a.addLog(log)
			},
			func(fileInfo *models.FileInfo) {
				runtime.EventsEmit(a.ctx, "file-list-updated")
			},
			a.stopChan,
		)

		a.isProcessing = false
		a.addLog(fmt.Sprintf("转换完成 - 成功: %d, 失败: %d", result.SuccessCount, result.FailedCount))
		runtime.EventsEmit(a.ctx, "conversion-completed", result)
	}()

	return nil
}

func (a *App) StopConversion() {
	if !a.isProcessing {
		return
	}

	select {
	case <-a.stopChan:
	default:
		close(a.stopChan)
	}

	a.addLog("正在停止转换任务...")
}

func (a *App) IsProcessing() bool {
	return a.isProcessing
}

func (a *App) GetLogs() []string {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	logs := make([]string, len(a.logEntries))
	copy(logs, a.logEntries)
	return logs
}

func (a *App) GetFileLogs(fileID int) []string {
	a.fileListMu.Lock()
	defer a.fileListMu.Unlock()

	for _, f := range a.fileList {
		if f.ID == fileID {
			return make([]string, len(f.Logs))
		}
	}
	return nil
}

func (a *App) addLog(log string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fullLog := fmt.Sprintf("[%s] %s", timestamp, log)
	a.logEntries = append(a.logEntries, fullLog)
	runtime.EventsEmit(a.ctx, "log-updated", fullLog)
}

func (a *App) GetAppDirectory() string {
	exePath, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(exePath)
}

func (a *App) OpenFileLocation(filePath string) error {
	dir := filepath.Dir(filePath)
	cmd := exec.Command("explorer", dir)
	return cmd.Start()
}

func (a *App) FormatFileSize(size int64) string {
	const unit = 1024
	if size < unit {
		return fmt.Sprintf("%d B", size)
	}
	div, exp := int64(unit), 0
	for n := size / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(size)/float64(div), "KMGTPE"[exp])
}
