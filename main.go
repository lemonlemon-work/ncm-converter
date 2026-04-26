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
	tableContainer := a.createFileTable()

	// 日志区域
	a.createLogArea()

	// 主布局：垂直分割，表格在上，日志在下
	split := container.NewVSplit(
		tableContainer,
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

func (a *NCMConverterApp) createFileTable() *fyne.Container {
	headers := []string{"ID", "路径", "曲名", "格式", "大小", "封面状态", "转码状态"}
	
	a.fileTable = widget.NewTable(
		func() (int, int) {
			a.fileListMu.Lock()
			defer a.fileListMu.Unlock()
			return len(a.fileList), 7
		},
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(id widget.TableCellID, cell fyne.CanvasObject) {
			label := cell.(*widget.Label)
			a.fileListMu.Lock()
			defer a.fileListMu.Unlock()
			if id.Row >= len(a.fileList) {
				return
			}
			file := a.fileList[id.Row]

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
		a.fileListMu.Lock()
		defer a.fileListMu.Unlock()
		if id.Row < len(a.fileList) {
			a.selectedFile = a.fileList[id.Row]
			a.updateLogDisplay()
		}
	}
	
	// 创建表头行
	headerRow := container.NewHBox()
	columnWidths := []float32{50, 200, 150, 60, 80, 100, 80}
	
	for i, header := range headers {
		headerLabel := widget.NewLabelWithStyle(header, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
		// 创建一个容器来固定宽度
		headerCell := container.NewHBox(
			widget.NewLabel(""), // 左边距占位
			headerLabel,
			widget.NewLabel(""), // 右边距占位
		)
		// 设置最小宽度
		headerCell.Resize(fyne.NewSize(columnWidths[i], headerLabel.MinSize().Height+4))
		headerRow.Add(headerCell)
	}
	
	// 创建表头背景
	headerBg := container.NewPadded(headerRow)
	
	// 将表头和表格组合
	tableContainer := container.NewBorder(headerBg, nil, nil, nil, a.fileTable)
	
	return tableContainer
}

func (a *NCMConverterApp) createLogArea() {
	a.logText = widget.NewMultiLineEntry()
	a.logText.Wrapping = fyne.TextWrapWord
	a.logText.SetPlaceHolder("日志将显示在这里...")
	a.logText.Disable()
}

func (a *NCMConverterApp) setupDragAndDrop() {
	a.window.SetOnDropped(func(pos fyne.Position, uris []fyne.URI) {
		for _, uri := range uris {
			path := uri.Path()
			info, err := os.Stat(path)
			if err != nil {
				a.addLog(fmt.Sprintf("无法访问路径: %s, 错误: %v", path, err))
				continue
			}
			if info.IsDir() {
				a.addFilesFromDirectory(path)
			} else {
				a.addFileIfNCM(path)
			}
		}
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
		a.addFilesFromDirectory(path)
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
		path := reader.URI().Path()
		reader.Close()
		a.addFileIfNCM(path)
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
	ncmFiles, err := converter.FindNCMFiles(dir)
	if err != nil {
		dialog.ShowError(fmt.Errorf("遍历目录失败: %v", err), a.window)
		return
	}

	if len(ncmFiles) == 0 {
		dialog.ShowInformation("提示", "没有找到NCM文件", a.window)
		return
	}

	for _, filePath := range ncmFiles {
		a.addFileToList(filePath)
	}

	a.addLog(fmt.Sprintf("从目录 %s 添加了 %d 个NCM文件", dir, len(ncmFiles)))
}

func (a *NCMConverterApp) addFileIfNCM(filePath string) {
	if strings.EqualFold(filepath.Ext(filePath), ".ncm") {
		a.addFileToList(filePath)
		a.addLog(fmt.Sprintf("添加文件: %s", filePath))
	} else {
		a.addLog(fmt.Sprintf("跳过非NCM文件: %s", filePath))
	}
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
