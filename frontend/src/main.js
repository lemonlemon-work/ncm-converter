// 全局变量
let fileList = [];
let selectedFileId = null;
let config = null;

// DOM 元素
const addDirBtn = document.getElementById('addDirBtn');
const addFileBtn = document.getElementById('addFileBtn');
const addAppDirBtn = document.getElementById('addAppDirBtn');
const settingsBtn = document.getElementById('settingsBtn');
const clearBtn = document.getElementById('clearBtn');
const startBtn = document.getElementById('startBtn');
const stopBtn = document.getElementById('stopBtn');
const statusLabel = document.getElementById('statusLabel');
const fileTableBody = document.getElementById('fileTableBody');
const logContent = document.getElementById('logContent');
const logFileInfo = document.getElementById('logFileInfo');

// 设置弹窗
const settingsModal = document.getElementById('settingsModal');
const closeSettings = document.getElementById('closeSettings');
const saveSettings = document.getElementById('saveSettings');
const cancelSettings = document.getElementById('cancelSettings');
const autoDownloadCover = document.getElementById('autoDownloadCover');
const maxDownloadConcurrency = document.getElementById('maxDownloadConcurrency');
const maxConvertConcurrency = document.getElementById('maxConvertConcurrency');

// 结果弹窗
const resultModal = document.getElementById('resultModal');
const closeResult = document.getElementById('closeResult');
const okResult = document.getElementById('okResult');
const successCount = document.getElementById('successCount');
const failedCount = document.getElementById('failedCount');
const skippedCount = document.getElementById('skippedCount');

// 右键菜单
const contextMenu = document.createElement('div');
contextMenu.className = 'context-menu hidden';
contextMenu.innerHTML = `
    <div class="context-menu-item" id="viewLogs">查看日志</div>
    <div class="context-menu-item" id="openLocation">打开文件位置</div>
`;
document.body.appendChild(contextMenu);

// 初始化
async function init() {
    // 加载配置
    config = await window.go.main.App.GetConfig();
    applyConfigToUI();

    // 加载文件列表
    await refreshFileList();

    // 绑定事件
    bindEvents();

    // 监听后端事件
    listenToEvents();
}

// 应用配置到 UI
function applyConfigToUI() {
    if (config) {
        autoDownloadCover.checked = config.autoDownloadCover;
        maxDownloadConcurrency.value = config.maxDownloadConcurrency;
        maxConvertConcurrency.value = config.maxConvertConcurrency;
    }
}

// 从 UI 获取配置
function getConfigFromUI() {
    return {
        autoDownloadCover: autoDownloadCover.checked,
        maxDownloadConcurrency: parseInt(maxDownloadConcurrency.value) || 3,
        maxConvertConcurrency: parseInt(maxConvertConcurrency.value) || 10
    };
}

// 刷新文件列表
async function refreshFileList() {
    fileList = await window.go.main.App.GetFileList();
    renderFileTable();
}

// 渲染文件表格
function renderFileTable() {
    fileTableBody.innerHTML = '';

    fileList.forEach(file => {
        const tr = document.createElement('tr');
        tr.dataset.id = file.id;
        tr.className = selectedFileId === file.id ? 'selected' : '';

        // 状态类名
        let coverClass = 'cover-notsupported';
        let statusClass = 'status-waiting';

        switch (file.coverStatus) {
            case '内置封面': coverClass = 'cover-builtin'; break;
            case '等待下载封面': coverClass = 'cover-waiting'; break;
            case '下载封面中': coverClass = 'cover-downloading'; break;
            case '下载完成': coverClass = 'cover-downloaded'; break;
        }

        switch (file.convertStatus) {
            case '转码中': statusClass = 'status-converting'; break;
            case '转码完成':
            case '合并完成': statusClass = 'status-converted'; break;
            case '错误': statusClass = 'status-error'; break;
            case '已跳过': statusClass = 'status-skipped'; break;
        }

        // 格式化文件大小
        const sizeStr = formatFileSize(file.size);

        tr.innerHTML = `
            <td class="col-id">${file.id}</td>
            <td class="col-path" title="${file.path}">${file.path}</td>
            <td class="col-song" title="${file.songName}">${file.songName}</td>
            <td class="col-format">${file.format || '-'}</td>
            <td class="col-size">${sizeStr}</td>
            <td class="col-cover ${coverClass}">${file.coverStatus}</td>
            <td class="col-status ${statusClass}">${file.convertStatus}</td>
        `;

        // 点击选中
        tr.addEventListener('click', () => {
            selectedFileId = file.id;
            renderFileTable();
            updateLogDisplay();
        });

        // 右键菜单
        tr.addEventListener('contextmenu', (e) => {
            e.preventDefault();
            selectedFileId = file.id;
            renderFileTable();
            showContextMenu(e.clientX, e.clientY);
        });

        fileTableBody.appendChild(tr);
    });
}

// 格式化文件大小
function formatFileSize(bytes) {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

// 更新日志显示
async function updateLogDisplay() {
    let logs;
    if (selectedFileId !== null) {
        logs = await window.go.main.App.GetFileLogs(selectedFileId);
        const file = fileList.find(f => f.id === selectedFileId);
        logFileInfo.textContent = file ? ` - ${file.songName}` : '';
    } else {
        logs = await window.go.main.App.GetLogs();
        logFileInfo.textContent = '';
    }

    logContent.innerHTML = logs.map(log => `<div>${escapeHtml(log)}</div>`).join('');
    logContent.scrollTop = logContent.scrollHeight;
}

// 转义 HTML
function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}

// 显示右键菜单
function showContextMenu(x, y) {
    contextMenu.style.left = x + 'px';
    contextMenu.style.top = y + 'px';
    contextMenu.classList.remove('hidden');
}

// 隐藏右键菜单
function hideContextMenu() {
    contextMenu.classList.add('hidden');
}

// 绑定事件
function bindEvents() {
    // 工具栏按钮
    addDirBtn.addEventListener('click', async () => {
        try {
            await window.go.main.App.AddDirectory();
            await refreshFileList();
        } catch (e) {
            console.error('添加目录失败:', e);
        }
    });

    addFileBtn.addEventListener('click', async () => {
        try {
            await window.go.main.App.AddFiles();
            await refreshFileList();
        } catch (e) {
            console.error('添加文件失败:', e);
        }
    });

    addAppDirBtn.addEventListener('click', async () => {
        try {
            await window.go.main.App.AddAppDirectory();
            await refreshFileList();
        } catch (e) {
            console.error('添加程序目录失败:', e);
        }
    });

    settingsBtn.addEventListener('click', () => {
        settingsModal.classList.remove('hidden');
    });

    clearBtn.addEventListener('click', async () => {
        await window.go.main.App.ClearFileList();
        selectedFileId = null;
        await refreshFileList();
        await updateLogDisplay();
    });

    startBtn.addEventListener('click', async () => {
        try {
            await window.go.main.App.StartConversion();
            updateUIForProcessing(true);
        } catch (e) {
            alert(e);
        }
    });

    stopBtn.addEventListener('click', async () => {
        await window.go.main.App.StopConversion();
    });

    // 设置弹窗
    closeSettings.addEventListener('click', () => {
        settingsModal.classList.add('hidden');
        applyConfigToUI();
    });

    cancelSettings.addEventListener('click', () => {
        settingsModal.classList.add('hidden');
        applyConfigToUI();
    });

    saveSettings.addEventListener('click', async () => {
        config = getConfigFromUI();
        await window.go.main.App.SaveConfig(config);
        settingsModal.classList.add('hidden');
    });

    // 结果弹窗
    closeResult.addEventListener('click', () => {
        resultModal.classList.add('hidden');
    });

    okResult.addEventListener('click', () => {
        resultModal.classList.add('hidden');
    });

    // 右键菜单
    document.getElementById('viewLogs').addEventListener('click', () => {
        hideContextMenu();
        updateLogDisplay();
    });

    document.getElementById('openLocation').addEventListener('click', async () => {
        hideContextMenu();
        const file = fileList.find(f => f.id === selectedFileId);
        if (file) {
            try {
                await window.go.main.App.OpenFileLocation(file.path);
            } catch (e) {
                console.error('打开文件位置失败:', e);
            }
        }
    });

    // 点击其他地方隐藏右键菜单
    document.addEventListener('click', (e) => {
        if (!contextMenu.contains(e.target)) {
            hideContextMenu();
        }
    });

    // 点击空白处取消选中
    document.querySelector('.table-container').addEventListener('click', (e) => {
        if (e.target.tagName === 'DIV') {
            selectedFileId = null;
            renderFileTable();
            updateLogDisplay();
        }
    });
}

// 更新处理中的 UI 状态
function updateUIForProcessing(isProcessing) {
    addDirBtn.disabled = isProcessing;
    addFileBtn.disabled = isProcessing;
    addAppDirBtn.disabled = isProcessing;
    settingsBtn.disabled = isProcessing;
    clearBtn.disabled = isProcessing;
    startBtn.disabled = isProcessing;
    stopBtn.disabled = !isProcessing;
    statusLabel.textContent = isProcessing ? '处理中...' : '就绪';
}

// 监听后端事件
function listenToEvents() {
    // 文件列表更新
    window.runtime.EventsOn('file-list-updated', async () => {
        await refreshFileList();
    });

    // 日志更新
    window.runtime.EventsOn('log-updated', async (log) => {
        if (selectedFileId === null) {
            const logDiv = document.createElement('div');
            logDiv.textContent = log;
            logContent.appendChild(logDiv);
            logContent.scrollTop = logContent.scrollHeight;
        }
    });

    // 转换完成
    window.runtime.EventsOn('conversion-completed', async (result) => {
        updateUIForProcessing(false);
        await refreshFileList();
        
        // 显示结果
        successCount.textContent = result.successCount;
        failedCount.textContent = result.failedCount;
        skippedCount.textContent = result.skippedCount;
        resultModal.classList.remove('hidden');
    });

    // 配置保存
    window.runtime.EventsOn('config-saved', () => {
        console.log('配置已保存');
    });
}

// 启动应用
if (window.go && window.go.main && window.go.main.App) {
    init();
} else {
    // 如果是开发环境（浏览器中），添加一些模拟数据
    console.log('Wails runtime not available - running in browser mode');
    // 显示提示
    document.body.innerHTML = '<div style="display:flex;align-items:center;justify-content:center;height:100vh;font-size:18px;color:#666;">请使用 Wails 运行此应用</div>';
}
