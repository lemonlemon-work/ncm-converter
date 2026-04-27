package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/lemonlemon-work/ncm-converter/converter"
	"github.com/lemonlemon-work/ncm-converter/models"
)

type NCMConverterApp struct {
	app          fyne.App
	window       fyne.Window
	config       *models.AppConfig
	converter    *converter.Converter
	fileList     []*models.FileInfo
	fileListMu   sync.Mutex
	nextID       int
	logEntries   []string
	logMu        sync.Mutex
	isProcessing bool
	stopChan     chan struct{}
	selectedFile *models.FileInfo

	// UI components
	fileTable    *widget.Table
	logText      *widget.Entry
	startBtn     *widget.Button
	stopBtn      *widget.Button
	addDirBtn    *widget.Button
	addFileBtn   *widget.Button
	addAppDirBtn *widget.Button
	settingsBtn  *widget.Button
	clearBtn     *widget.Button
}

func NewNCMConverterApp() *NCMConverterApp {
	config := models.DefaultConfig()
	return &NCMConverterApp{
		config:    config,
		converter: converter.NewConverter(config),
		nextID:    1,
		stopChan:  make(chan struct{}),
	}
}

func (a *NCMConverterApp) Run() {
	a.app = app.NewWithID("com.ncm.converter")
	a.window = a.app.NewWindow("NCM 转换器")
	a.window.Resize(fyne.NewSize(1000, 700))

	a.setupUI()
	a.setupDragAndDrop()

	a.window.ShowAndRun()
}

func (a *NCMConverterApp) setupUI() {
	// 顶部工具栏
	toolbar := a.createToolbar()

	// 文件表格（包含表头）
	a.createFileTable()

	// 日志区域
	a.createLogArea()

	// 主布局：垂直分割，表格在上，日志在下
	split := container.NewVSplit(
		container.NewBorder(nil, nil, nil, nil, a.fileTable),
		container.NewBorder(nil, nil, nil, nil, a.logText),
	)
	split.Offset = 0.7

	mainContainer := container.NewBorder(toolbar, nil, nil, nil, split)
	a.window.SetContent(mainContainer)
}

func (a *NCMConverterApp) createToolbar() *fyne.Container {
	a.addDirBtn = widget.NewButtonWithIcon("添加目录", theme.FolderIcon(), a.addDirectory)
	a.addFileBtn = widget.NewButtonWithIcon("添加文件", theme.FileIcon(), a.addFiles)
	a.addAppDirBtn = widget.NewButtonWithIcon("添加程序目录", theme.HomeIcon(), a.addAppDirectory)
	a.settingsBtn = widget.NewButtonWithIcon("设置", theme.SettingsIcon(), a.showSettings)
	a.clearBtn = widget.NewButtonWithIcon("清空列表", theme.DeleteIcon(), a.clearFileList)
	a.startBtn = widget.NewButtonWithIcon("开始转码", theme.MediaPlayIcon(), a.startConversion)
	a.startBtn.Importance = widget.HighImportance
	a.stopBtn = widget.NewButtonWithIcon("停止", theme.MediaStopIcon(), a.stopConversion)
	a.stopBtn.Disable()

	// 状态标签
	statusLabel := widget.NewLabel("就绪")

	toolbar := container.NewHBox(
		a.addDirBtn,
		a.addFileBtn,
		a.addAppDirBtn,
		widget.NewSeparator(),
		a.settingsBtn,
		a.clearBtn,
		widget.NewSeparator(),
		a.startBtn,
		a.stopBtn,
		layout.NewSpacer(),
		statusLabel,
	)

	return toolbar
}

func (a *NCMConverterApp) createFileTable() *widget.Table {
	headers := []string{"ID", "路径", "曲名", "格式", "大小", "封面状态", "转码状态"}
	
	a.fileTable = widget.NewTable(
		func() (int, int) {
			a.fileListMu.Lock()
			defer a.fileListMu.Unlock()
			// 第0行是表头，后面是实际数据
			return len(a.fileList) + 1, 7
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			
			// 第0行是表头
			if id.Row == 0 {
				if id.Col < len(headers) {
					label.TextStyle = fyne.TextStyle{Bold: true}
					label.SetText(headers[id.Col])
				}
				return
			}
			
			// 实际数据行（从第1行开始）
			dataRow := id.Row - 1
			a.fileListMu.Lock()
			defer a.fileListMu.Unlock()
			
			if dataRow >= len(a.fileList) {
				return
			}
			
			file := a.fileList[dataRow]
			label.TextStyle = fyne.TextStyle{}

			switch id.Col {
			case 0:
				label.SetText(fmt.Sprintf("%d", file.ID))
			case 1:
				label.SetText(file.Path)
			case 2:
				label.SetText(file.SongName)
			case 3:
				label.SetText(file.Format)
			case 4:
				label.SetText(a.formatFileSize(file.Size))
			case 5:
				label.SetText(string(file.CoverStatus))
			case 6:
				label.SetText(string(file.ConvertStatus))
			}
		},
	)

	// 设置列宽
	a.fileTable.SetColumnWidth(0, 50)
	a.fileTable.SetColumnWidth(1, 200)
	a.fileTable.SetColumnWidth(2, 150)
	a.fileTable.SetColumnWidth(3, 60)
	a.fileTable.SetColumnWidth(4, 80)
	a.fileTable.SetColumnWidth(5, 100)
	a.fileTable.SetColumnWidth(6, 80)

	// 选中事件
	a.fileTable.OnSelected = func(id widget.TableCellID) {
		// 忽略表头行的选中
		if id.Row == 0 {
			return
		}
		
		dataRow := id.Row - 1
		a.fileListMu.Lock()
		defer a.fileListMu.Unlock()
		
		if dataRow >= 0 && dataRow < len(a.fileList) {
			a.selectedFile = a.fileList[dataRow]
			a.updateLogDisplay()
		}
	}
	
	return a.fileTable
}

func (a *NCMConverterApp) createLogArea() {
	a.logText = widget.NewMultiLineEntry()
	a.logText.Wrapping = fyne.TextWrapWord
	a.logText.SetPlaceHolder("日志将显示在这里...")
	a.logText.Disable()
}

func (a *NCMConverterApp) setupDragAndDrop() {
	a.window.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		// 收集所有文件和目录，避免阻塞主线程
		go func() {
			var files []string
			var dirs []string

			for _, uri := range uris {
				path := uri.Path()
				info, err := os.Stat(path)
				if err != nil {
					fyne.Do(func() {
						a.addLog(fmt.Sprintf("无法访问路径: %s, 错误: %v", path, err))
					})
					continue
				}
				if info.IsDir() {
					dirs = append(dirs, path)
				} else {
					files = append(files, path)
				}
			}

			// 批量处理文件
			if len(files) > 0 {
				a.addFilesBatch(files)
			}

			// 处理目录（每个目录单独处理，因为目录需要遍历）
			for _, dir := range dirs {
				a.addFilesFromDirectory(dir)
			}
		}()
	})
}

func (a *NCMConverterApp) addDirectory() {
	dialog.ShowFolderOpen(func(list fyne.ListableURI, err error) {
		if err != nil {
			dialog.ShowError(err, a.window)
			return
		}
		if list == nil {
			return
		}
		path := list.Path()
		// 在后台 goroutine 中执行耗时操作
		go func() {
			a.addFilesFromDirectory(path)
		}()
	}, a.window)
}

func (a *NCMConverterApp) addFiles() {
	fd := dialog.NewFileOpen(func(reader fyne.URIReadCloser, err error) {
		if err != nil {
			dialog.ShowError(err, a.window)
			return
		}
		if reader == nil {
			return
		}
		// 先获取路径
		path := reader.URI().Path()
		// 立即关闭 reader，因为我们只需要路径
		reader.Close()
		
		// 在后台 goroutine 中处理文件
		go func(filePath string) {
			a.addFileIfNCM(filePath)
		}(path)
	}, a.window)
	fd.SetFilter(storage.NewExtensionFileFilter([]string{".ncm"}))
	fd.Show()
}

func (a *NCMConverterApp) addAppDirectory() {
	exePath, err := os.Executable()
	if err != nil {
		dialog.ShowError(fmt.Errorf("获取可执行文件路径失败: %v", err), a.window)
		return
	}
	appDir := filepath.Dir(exePath)
	a.addFilesFromDirectory(appDir)
}

func (a *NCMConverterApp) addFilesFromDirectory(dir string) {
	// 在后台 goroutine 中执行所有文件操作
	go func() {
		// 第一阶段：快速扫描目录获取 NCM 文件列表
		ncmFiles, err := converter.FindNCMFiles(dir)
		if err != nil {
			fyne.Do(func() {
				dialog.ShowError(fmt.Errorf("遍历目录失败: %v", err), a.window)
			})
			return
		}

		if len(ncmFiles) == 0 {
			fyne.Do(func() {
				dialog.ShowInformation("提示", "没有找到NCM文件", a.window)
			})
			return
		}

		// 第二阶段：过滤掉已存在的文件
		a.fileListMu.Lock()
		existingPaths := make(map[string]bool)
		for _, f := range a.fileList {
			existingPaths[f.Path] = true
		}
		a.fileListMu.Unlock()

		var newFilePaths []string
		for _, filePath := range ncmFiles {
			if !existingPaths[filePath] {
				newFilePaths = append(newFilePaths, filePath)
				existingPaths[filePath] = true
			}
		}

		if len(newFilePaths) == 0 {
			fyne.Do(func() {
				a.addLog(fmt.Sprintf("目录 %s 中没有新的NCM文件", dir))
			})
			return
		}

		// 第三阶段：读取元信息（文件大小等）
		// 使用限制并发数的方式，避免同时打开太多文件
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

				// 读取文件大小
				size, err := converter.GetFileSize(fp)
				if err != nil {
					size = 0
				}

				fileInfo := &models.FileInfo{
					ID:            startID + idx,
					Path:          fp,
					SongName:      converter.GetSongName(fp),
					Format:        "",
					Size:          size,
					CoverStatus:   models.CoverStatusNotSupported,
					ConvertStatus: models.ConvertStatusWaiting,
					Logs:          []string{},
				}

				newFilesMu.Lock()
				newFiles = append(newFiles, fileInfo)
				newFilesMu.Unlock()
			}(filePath, i, currentID)
		}

		wg.Wait()

		// 第四阶段：在主线程中一次性更新UI
		if len(newFiles) > 0 {
			fyne.Do(func() {
				a.fileListMu.Lock()
				// 再次过滤掉可能在后台处理时已被添加的文件
				existingPaths := make(map[string]bool)
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
					// 更新 nextID
					maxID := a.nextID
					for _, f := range filesToAdd {
						if f.ID >= maxID {
							maxID = f.ID + 1
						}
					}
					a.nextID = maxID
				}
				a.fileListMu.Unlock()

				// 只刷新一次表格
				a.fileTable.Refresh()
				a.addLog(fmt.Sprintf("从目录 %s 添加了 %d 个NCM文件", dir, len(filesToAdd)))
			})
		}
	}()
}

// addFilesBatch 批量添加多个文件，避免为每个文件创建 goroutine
func (a *NCMConverterApp) addFilesBatch(filePaths []string) {
	if len(filePaths) == 0 {
		return
	}

	// 在后台 goroutine 中处理
	go func() {
		// 第一阶段：过滤掉已存在的文件和非NCM文件
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
			return
		}

		// 第二阶段：读取元信息（文件大小等）
		// 使用限制并发数的方式
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

				size, err := converter.GetFileSize(fp)
				if err != nil {
					size = 0
				}

				fileInfo := &models.FileInfo{
					ID:            startID + idx,
					Path:          fp,
					SongName:      converter.GetSongName(fp),
					Format:        "",
					Size:          size,
					CoverStatus:   models.CoverStatusNotSupported,
					ConvertStatus: models.ConvertStatusWaiting,
					Logs:          []string{},
				}

				newFilesMu.Lock()
				newFiles = append(newFiles, fileInfo)
				newFilesMu.Unlock()
			}(filePath, i, currentID)
		}

		wg.Wait()

		// 第三阶段：在主线程中一次性更新UI
		if len(newFiles) > 0 {
			fyne.Do(func() {
				a.fileListMu.Lock()
				// 再次过滤
				existingPaths := make(map[string]bool)
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

				a.fileTable.Refresh()
				a.addLog(fmt.Sprintf("批量添加了 %d 个NCM文件", len(filesToAdd)))
			})
		}
	}()
}

func (a *NCMConverterApp) addFileIfNCM(filePath string) {
	// 单个文件也使用批量处理函数，保持一致性
	a.addFilesBatch([]string{filePath})
}

func (a *NCMConverterApp) addFileToList(filePath string) {
	a.fileListMu.Lock()
	defer a.fileListMu.Unlock()

	// 检查是否已存在
	for _, existing := range a.fileList {
		if existing.Path == filePath {
			return
		}
	}

	size, err := converter.GetFileSize(filePath)
	if err != nil {
		size = 0
	}

	fileInfo := &models.FileInfo{
		ID:            a.nextID,
		Path:          filePath,
		SongName:      converter.GetSongName(filePath),
		Format:        "",
		Size:          size,
		CoverStatus:   models.CoverStatusNotSupported,
		ConvertStatus: models.ConvertStatusWaiting,
		Logs:          []string{},
	}

	a.fileList = append(a.fileList, fileInfo)
	a.nextID++

	// 更新UI
	a.fileTable.Refresh()
}

func (a *NCMConverterApp) removeFile(index int) {
	a.fileListMu.Lock()
	defer a.fileListMu.Unlock()
	if index >= 0 && index < len(a.fileList) {
		a.fileList = append(a.fileList[:index], a.fileList[index+1:]...)
		a.fileTable.Refresh()
	}
}

func (a *NCMConverterApp) clearFileList() {
	a.fileListMu.Lock()
	defer a.fileListMu.Unlock()
	a.fileList = []*models.FileInfo{}
	a.nextID = 1
	a.selectedFile = nil
	a.fileTable.Refresh()
	a.addLog("已清空文件列表")
	a.updateLogDisplay()
}

func (a *NCMConverterApp) startConversion() {
	a.fileListMu.Lock()
	if len(a.fileList) == 0 {
		a.fileListMu.Unlock()
		dialog.ShowInformation("提示", "请先添加NCM文件", a.window)
		return
	}
	a.fileListMu.Unlock()

	if a.isProcessing {
		return
	}

	a.isProcessing = true
	a.startBtn.Disable()
	a.stopBtn.Enable()
	a.addDirBtn.Disable()
	a.addFileBtn.Disable()
	a.addAppDirBtn.Disable()
	a.settingsBtn.Disable()
	a.clearBtn.Disable()

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
				a.fileTable.Refresh()
			},
			a.stopChan,
		)

		// 更新UI状态
		a.isProcessing = false
		a.startBtn.Enable()
		a.stopBtn.Disable()
		a.addDirBtn.Enable()
		a.addFileBtn.Enable()
		a.addAppDirBtn.Enable()
		a.settingsBtn.Enable()
		a.clearBtn.Enable()

		// 显示结果
		a.showResult(result)
	}()
}

func (a *NCMConverterApp) stopConversion() {
	if !a.isProcessing {
		return
	}

	select {
	case <-a.stopChan:
		// 已经关闭
	default:
		close(a.stopChan)
	}

	a.addLog("正在停止转换任务...")
}

func (a *NCMConverterApp) showResult(result *models.ConvertResult) {
	message := fmt.Sprintf(
		"转换完成！\n\n成功: %d 个\n失败: %d 个",
		result.SuccessCount,
		result.FailedCount,
	)
	dialog.ShowInformation("转换结果", message, a.window)
	a.addLog(fmt.Sprintf("转换完成 - 成功: %d, 失败: %d", result.SuccessCount, result.FailedCount))
}

func (a *NCMConverterApp) showSettings() {
	// 创建设置对话框
	autoDownloadCheck := widget.NewCheck("自动下载封面", nil)
	autoDownloadCheck.SetChecked(a.config.AutoDownloadCover)

	maxDownloadEntry := widget.NewEntry()
	maxDownloadEntry.SetText(fmt.Sprintf("%d", a.config.MaxDownloadConcurrency))

	maxConvertEntry := widget.NewEntry()
	maxConvertEntry.SetText(fmt.Sprintf("%d", a.config.MaxConvertConcurrency))

	form := container.NewVBox(
		widget.NewForm(
			widget.NewFormItem("自动下载封面", autoDownloadCheck),
			widget.NewFormItem("最大下载并发数", maxDownloadEntry),
			widget.NewFormItem("最大转码线程数", maxConvertEntry),
		),
	)

	dialog.ShowCustomConfirm("设置", "保存", "取消", form, func(save bool) {
		if save {
			// 保存设置
			a.config.AutoDownloadCover = autoDownloadCheck.Checked

			// 解析并发数
			var maxDownload, maxConvert int
			fmt.Sscanf(maxDownloadEntry.Text, "%d", &maxDownload)
			fmt.Sscanf(maxConvertEntry.Text, "%d", &maxConvert)

			if maxDownload > 0 {
				a.config.MaxDownloadConcurrency = maxDownload
			}
			if maxConvert > 0 {
				a.config.MaxConvertConcurrency = maxConvert
			}

			// 更新转换器配置
			a.converter = converter.NewConverter(a.config)
			a.addLog("设置已保存")
		}
	}, a.window)
}

func (a *NCMConverterApp) addLog(log string) {
	a.logMu.Lock()
	defer a.logMu.Unlock()
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	fullLog := fmt.Sprintf("[%s] %s", timestamp, log)
	a.logEntries = append(a.logEntries, fullLog)
	a.updateLogDisplay()
}

func (a *NCMConverterApp) updateLogDisplay() {
	var logs []string
	if a.selectedFile != nil {
		logs = a.selectedFile.GetLogs()
	} else {
		a.logMu.Lock()
		logs = make([]string, len(a.logEntries))
		copy(logs, a.logEntries)
		a.logMu.Unlock()
	}

	fullText := strings.Join(logs, "\n")
	a.logText.SetText(fullText)
	// 滚动到底部
	a.logText.CursorRow = len(logs)
}

func (a *NCMConverterApp) formatFileSize(size int64) string {
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

func main() {
	app := NewNCMConverterApp()
	app.Run()
}
