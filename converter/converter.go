package converter

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lemonlemon-work/ncm-converter/models"
)

var musicSuffixList = []string{"mp3", "wav", "ape", "flac"}

type NCMMetadata struct {
	Format   string `json:"format"`
	AlbumPic string `json:"albumPic"`
}

// Converter 负责处理NCM文件的转换
type Converter struct {
	config *models.AppConfig
	logMu  sync.Mutex
}

// NewConverter 创建一个新的转换器
func NewConverter(config *models.AppConfig) *Converter {
	return &Converter{
		config: config,
	}
}

// ProcessFile 处理单个NCM文件
func (c *Converter) ProcessFile(fileInfo *models.FileInfo, logCallback func(string)) error {
	filePath := fileInfo.Path

	if fileInfo.ConvertStatus == models.ConvertStatusConverted {
		logCallback(fmt.Sprintf("文件已转换完成，跳过: %s", filePath))
		return nil
	}

	if fileInfo.ConvertStatus == models.ConvertStatusConverting {
		logCallback(fmt.Sprintf("文件正在转换中，跳过: %s", filePath))
		return nil
	}

	fileInfo.ConvertStatus = models.ConvertStatusConverting
	logCallback(fmt.Sprintf("开始转换文件: %s", filePath))

	baseName := strings.TrimSuffix(filepath.Base(filePath), filepath.Ext(filePath))

	err := c.dump(fileInfo, baseName, logCallback)
	if err != nil {
		logCallback(fmt.Sprintf("转换文件失败: %s, 错误: %v", filePath, err))
		fileInfo.ConvertStatus = models.ConvertStatusError
		return err
	} else {
		logCallback(fmt.Sprintf("转换文件成功: %s", filePath))
		fileInfo.ConvertStatus = models.ConvertStatusConverted
		return nil
	}
}

func (c *Converter) dump(fileInfo *models.FileInfo, fileNameNoSuffix string, logCallback func(string)) error {
	coreKey := []byte{0x68, 0x7A, 0x48, 0x52, 0x41, 0x6D, 0x73, 0x6F, 0x35, 0x6B, 0x49, 0x6E, 0x62, 0x61, 0x78, 0x57}
	metaKey := []byte{0x23, 0x31, 0x34, 0x6C, 0x6A, 0x6B, 0x5F, 0x21, 0x5C, 0x5D, 0x26, 0x30, 0x55, 0x3C, 0x27, 0x28}

	filePath := fileInfo.Path
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("无法打开文件: %v", err)
	}
	defer f.Close()

	header := make([]byte, 8)
	_, err = io.ReadFull(f, header)
	if err != nil {
		return fmt.Errorf("读取头部失败: %v", err)
	}
	if !bytes.Equal(header, []byte("CTENFDAM")) {
		return fmt.Errorf("不是有效的NCM文件")
	}

	_, err = f.Seek(2, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("跳过字节失败: %v", err)
	}

	keyLengthBytes := make([]byte, 4)
	_, err = io.ReadFull(f, keyLengthBytes)
	if err != nil {
		return fmt.Errorf("读取key长度失败: %v", err)
	}
	keyLength := binary.LittleEndian.Uint32(keyLengthBytes)

	keyData := make([]byte, keyLength)
	_, err = io.ReadFull(f, keyData)
	if err != nil {
		return fmt.Errorf("读取key数据失败: %v", err)
	}

	for i := range keyData {
		keyData[i] ^= 0x64
	}

	keyData, err = aesECBDecrypt(coreKey, keyData)
	if err != nil {
		return fmt.Errorf("解密key数据失败: %v", err)
	}

	keyData = pkcs7Unpad(keyData)
	if len(keyData) > 17 {
		keyData = keyData[17:]
	}
	keyLength = uint32(len(keyData))

	keyBox := make([]byte, 256)
	for i := range keyBox {
		keyBox[i] = byte(i)
	}

	var cVal uint8 = 0
	var lastByte uint8 = 0
	var keyOffset uint32 = 0

	for i := 0; i < 256; i++ {
		swap := keyBox[i]
		cVal = (swap + lastByte + keyData[keyOffset]) & 0xff
		keyOffset++
		if keyOffset >= keyLength {
			keyOffset = 0
		}
		keyBox[i] = keyBox[cVal]
		keyBox[cVal] = swap
		lastByte = cVal
	}

	metaLengthBytes := make([]byte, 4)
	_, err = io.ReadFull(f, metaLengthBytes)
	if err != nil {
		return fmt.Errorf("读取元数据长度失败: %v", err)
	}
	metaLength := binary.LittleEndian.Uint32(metaLengthBytes)

	var metadata NCMMetadata
	var imageData []byte

	if metaLength > 0 {
		metaData := make([]byte, metaLength)
		_, err = io.ReadFull(f, metaData)
		if err != nil {
			return fmt.Errorf("读取元数据失败: %v", err)
		}

		for i := range metaData {
			metaData[i] ^= 0x63
		}

		if len(metaData) > 22 {
			metaData = metaData[22:]
			decodedMeta := make([]byte, base64.StdEncoding.DecodedLen(len(metaData)))
			n, err := base64.StdEncoding.Decode(decodedMeta, metaData)
			if err != nil {
				return fmt.Errorf("Base64解码元数据失败: %v", err)
			}
			metaData = decodedMeta[:n]

			metaData, err = aesECBDecrypt(metaKey, metaData)
			if err != nil {
				return fmt.Errorf("解密元数据失败: %v", err)
			}

			metaData = pkcs7Unpad(metaData)
			if len(metaData) > 6 {
				metaData = metaData[6:]
			}

			err = json.Unmarshal(metaData, &metadata)
			if err != nil {
				return fmt.Errorf("解析元数据JSON失败: %v", err)
			}
		}
	}

	_, err = f.Seek(4, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("跳过CRC32失败: %v", err)
	}

	_, err = f.Seek(5, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("跳过字节失败: %v", err)
	}

	imageSizeBytes := make([]byte, 4)
	_, err = io.ReadFull(f, imageSizeBytes)
	if err != nil {
		return fmt.Errorf("读取图片大小失败: %v", err)
	}
	imageSize := binary.LittleEndian.Uint32(imageSizeBytes)

	if imageSize > 0 {
		imageData = make([]byte, imageSize)
		_, err = io.ReadFull(f, imageData)
		if err != nil {
			return fmt.Errorf("读取图片数据失败: %v", err)
		}
		fileInfo.CoverStatus = models.CoverStatusBuiltIn
	} else if metadata.AlbumPic != "" && c.config.AutoDownloadCover {
		fileInfo.CoverStatus = models.CoverStatusWaitingDownload
	} else {
		fileInfo.CoverStatus = models.CoverStatusNotSupported
	}

	var outputFileName string
	if metadata.Format != "" {
		outputFileName = fileNameNoSuffix + "." + metadata.Format
		fileInfo.Format = metadata.Format
	} else {
		outputFileName = fileNameNoSuffix + ".mp3"
		metadata.Format = "mp3"
		fileInfo.Format = "mp3"
	}

	outputFilePath := filepath.Join(filepath.Dir(filePath), outputFileName)

	outFile, err := os.Create(outputFilePath)
	if err != nil {
		return fmt.Errorf("创建输出文件失败: %v", err)
	}

	chunk := make([]byte, 0x8000)
	for {
		n, err := f.Read(chunk)
		if err != nil && err != io.EOF {
			outFile.Close()
			return fmt.Errorf("读取音频数据失败: %v", err)
		}
		if n == 0 {
			break
		}

		for i := 1; i <= n; i++ {
			j := i & 0xff
			chunk[i-1] ^= keyBox[(keyBox[j]+keyBox[(keyBox[j]+byte(j))&0xff])&0xff]
		}

		_, err = outFile.Write(chunk[:n])
		if err != nil {
			outFile.Close()
			return fmt.Errorf("写入音频数据失败: %v", err)
		}
	}

	outFile.Close()

	if len(imageData) > 0 {
		logCallback(fmt.Sprintf("使用NCM文件中提取的封面图片，大小: %d 字节", len(imageData)))
		if !c.embedCover(outputFilePath, imageData, metadata.Format, logCallback) {
			logCallback(fmt.Sprintf("封面嵌入失败，音频文件已保存: %s", outputFilePath))
		}
	} else if metadata.AlbumPic != "" && c.config.AutoDownloadCover {
		fileInfo.CoverStatus = models.CoverStatusDownloading
		logCallback("NCM文件中没有找到封面图片，尝试从网络下载...")
		resp, err := http.Get(metadata.AlbumPic)
		if err != nil {
			logCallback(fmt.Sprintf("从网络下载封面图片出错: %v", err))
			fileInfo.CoverStatus = models.CoverStatusNotSupported
		} else {
			defer resp.Body.Close()
			if resp.StatusCode != 200 {
				logCallback(fmt.Sprintf("从网络下载封面图片失败，状态码: %d", resp.StatusCode))
				fileInfo.CoverStatus = models.CoverStatusNotSupported
			} else {
				coverData, err := io.ReadAll(resp.Body)
				if err != nil {
					logCallback(fmt.Sprintf("读取网络封面图片数据失败: %v", err))
					fileInfo.CoverStatus = models.CoverStatusNotSupported
				} else {
					logCallback(fmt.Sprintf("从网络下载封面图片成功，大小: %d 字节", len(coverData)))
					fileInfo.CoverStatus = models.CoverStatusDownloaded
					if !c.embedCover(outputFilePath, coverData, metadata.Format, logCallback) {
						logCallback(fmt.Sprintf("封面嵌入失败，音频文件已保存: %s", outputFilePath))
					}
				}
			}
		}
	} else {
		logCallback(fmt.Sprintf("未找到封面图片，音频文件已保存（无封面）: %s", outputFilePath))
	}

	return nil
}

func (c *Converter) embedCover(audioPath string, coverData []byte, format string, logCallback func(string)) bool {
	if len(coverData) == 0 {
		logCallback("封面数据为空，无法嵌入")
		return false
	}

	format = strings.ToLower(format)

	supportedEmbedFormats := map[string]bool{
		"mp3":  true,
		"flac": true,
	}

	if !supportedEmbedFormats[format] {
		logCallback(fmt.Sprintf("%s格式不支持嵌入封面，将封面保存为单独文件", strings.ToUpper(format)))
		coverPath := audioPath + ".cover.jpg"
		if err := os.WriteFile(coverPath, coverData, 0644); err == nil {
			logCallback(fmt.Sprintf("封面图片已保存到: %s", coverPath))
			return true
		}
		return false
	}

	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		logCallback("未找到ffmpeg，无法嵌入封面。请安装ffmpeg并添加到系统PATH中。")
		coverPath := audioPath + ".cover.jpg"
		if err := os.WriteFile(coverPath, coverData, 0644); err == nil {
			logCallback(fmt.Sprintf("封面图片已保存到: %s", coverPath))
			return true
		}
		return false
	}

	tempCoverPath := audioPath + ".temp_cover.jpg"
	if err := os.WriteFile(tempCoverPath, coverData, 0644); err != nil {
		logCallback(fmt.Sprintf("保存临时封面文件失败: %v", err))
		return false
	}
	defer func() {
		if _, err := os.Stat(tempCoverPath); err == nil {
			os.Remove(tempCoverPath)
		}
	}()

	tempOutputPath := audioPath + ".temp_output" + filepath.Ext(audioPath)

	if _, err := os.Stat(tempOutputPath); err == nil {
		os.Remove(tempOutputPath)
	}

	var cmd *exec.Cmd
	if format == "mp3" {
		cmd = exec.Command("ffmpeg", "-i", audioPath, "-i", tempCoverPath,
			"-map", "0:a", "-map", "1:v", "-c", "copy",
			"-id3v2_version", "3",
			"-metadata:s:v", "title=Album cover",
			"-metadata:s:v", "comment=Cover (front)",
			"-disposition:v", "attached_pic",
			"-y", tempOutputPath)
	} else {
		cmd = exec.Command("ffmpeg", "-i", audioPath, "-i", tempCoverPath,
			"-map", "0:a", "-map", "1:v", "-c", "copy",
			"-metadata:s:v", "title=Album cover",
			"-metadata:s:v", "comment=Cover (front)",
			"-disposition:v", "attached_pic",
			"-y", tempOutputPath)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	logCallback("执行ffmpeg命令嵌入封面...")
	err = cmd.Run()
	if err != nil {
		logCallback(fmt.Sprintf("ffmpeg执行失败: %v", err))
		logCallback(fmt.Sprintf("ffmpeg stdout: %s", stdout.String()))
		logCallback(fmt.Sprintf("ffmpeg stderr: %s", stderr.String()))
		coverPath := audioPath + ".cover.jpg"
		if err := os.WriteFile(coverPath, coverData, 0644); err == nil {
			logCallback(fmt.Sprintf("封面图片已保存到: %s", coverPath))
			return true
		}
		return false
	}

	if _, err := os.Stat(tempOutputPath); os.IsNotExist(err) {
		logCallback("ffmpeg执行成功但未生成输出文件，可能是封面嵌入不支持")
		coverPath := audioPath + ".cover.jpg"
		if err := os.WriteFile(coverPath, coverData, 0644); err == nil {
			logCallback(fmt.Sprintf("封面图片已保存到: %s", coverPath))
			return true
		}
		return false
	}

	logCallback("替换原文件...")
	if err := os.Remove(audioPath); err != nil {
		logCallback(fmt.Sprintf("删除原文件失败: %v", err))
		if _, err := os.Stat(tempOutputPath); err == nil {
			os.Remove(tempOutputPath)
		}
		return false
	}

	if err := os.Rename(tempOutputPath, audioPath); err != nil {
		logCallback(fmt.Sprintf("重命名临时文件失败: %v", err))
		return false
	}

	logCallback(fmt.Sprintf("封面已成功嵌入到%s文件: %s", strings.ToUpper(format), audioPath))
	return true
}

func aesECBDecrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	if len(ciphertext)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度不是块大小的倍数")
	}

	plaintext := make([]byte, len(ciphertext))
	mode := NewECBDecrypter(block)
	mode.CryptBlocks(plaintext, ciphertext)

	return plaintext, nil
}

type ecbDecrypter struct {
	b         cipher.Block
	blockSize int
}

func NewECBDecrypter(b cipher.Block) cipher.BlockMode {
	return &ecbDecrypter{
		b:         b,
		blockSize: b.BlockSize(),
	}
}

func (x *ecbDecrypter) BlockSize() int {
	return x.blockSize
}

func (x *ecbDecrypter) CryptBlocks(dst, src []byte) {
	if len(src)%x.blockSize != 0 {
		panic("crypto/cipher: input not full blocks")
	}
	if len(dst) < len(src) {
		panic("crypto/cipher: output smaller than input")
	}

	for len(src) > 0 {
		x.b.Decrypt(dst[:x.blockSize], src[:x.blockSize])
		src = src[x.blockSize:]
		dst = dst[x.blockSize:]
	}
}

func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	padding := int(data[len(data)-1])
	if padding > len(data) || padding == 0 {
		return data
	}
	return data[:len(data)-padding]
}

// FindNCMFiles 查找指定目录下的所有NCM文件
func FindNCMFiles(rootDir string) ([]string, error) {
	var ncmFiles []string
	err := filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.EqualFold(filepath.Ext(path), ".ncm") {
			ncmFiles = append(ncmFiles, path)
		}
		return nil
	})
	return ncmFiles, err
}

// GetFileSize 获取文件大小
func GetFileSize(filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// GetSongName 从文件名获取歌曲名
func GetSongName(filePath string) string {
	baseName := filepath.Base(filePath)
	return strings.TrimSuffix(baseName, filepath.Ext(baseName))
}

// ProcessFilesConcurrently 并发处理多个文件
func (c *Converter) ProcessFilesConcurrently(fileInfos []*models.FileInfo, 
	logCallback func(string), 
	progressCallback func(*models.FileInfo),
	stopChan <-chan struct{}) *models.ConvertResult {
	
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, c.config.MaxConvertConcurrency)
	var result models.ConvertResult
	var resultMu sync.Mutex

	for _, fileInfo := range fileInfos {
		select {
		case <-stopChan:
			logCallback("转换任务已停止")
			return &result
		default:
		}

		wg.Add(1)
		go func(fi *models.FileInfo) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			select {
			case <-stopChan:
				return
			default:
			}

			err := c.ProcessFile(fi, func(log string) {
				fi.AddLog(log)
				logCallback(log)
			})

			resultMu.Lock()
			if err != nil {
				result.FailedCount++
			} else {
				result.SuccessCount++
			}
			resultMu.Unlock()

			if progressCallback != nil {
				progressCallback(fi)
			}
		}(fileInfo)
	}

	wg.Wait()
	return &result
}

// NCMFileInfo 包含从NCM文件中提取的快速信息
type NCMFileInfo struct {
	SongName    string
	Format      string
	CoverStatus models.CoverStatus
	HasCover    bool
	CoverURL    string
}

// GetNCMFileInfo 快速解析NCM文件，获取歌曲信息和封面状态
func GetNCMFileInfo(filePath string) (*NCMFileInfo, error) {
	coreKey := []byte{0x68, 0x7A, 0x48, 0x52, 0x41, 0x6D, 0x73, 0x6F, 0x35, 0x6B, 0x49, 0x6E, 0x62, 0x61, 0x78, 0x57}
	metaKey := []byte{0x23, 0x31, 0x34, 0x6C, 0x6A, 0x6B, 0x5F, 0x21, 0x5C, 0x5D, 0x26, 0x30, 0x55, 0x3C, 0x27, 0x28}

	result := &NCMFileInfo{
		SongName:    GetSongName(filePath),
		Format:      "",
		CoverStatus: models.CoverStatusNotSupported,
		HasCover:    false,
		CoverURL:    "",
	}

	f, err := os.Open(filePath)
	if err != nil {
		return result, fmt.Errorf("无法打开文件: %v", err)
	}
	defer f.Close()

	header := make([]byte, 8)
	_, err = io.ReadFull(f, header)
	if err != nil {
		return result, fmt.Errorf("读取头部失败: %v", err)
	}
	if !bytes.Equal(header, []byte("CTENFDAM")) {
		return result, fmt.Errorf("不是有效的NCM文件")
	}

	_, err = f.Seek(2, io.SeekCurrent)
	if err != nil {
		return result, fmt.Errorf("跳过字节失败: %v", err)
	}

	keyLengthBytes := make([]byte, 4)
	_, err = io.ReadFull(f, keyLengthBytes)
	if err != nil {
		return result, fmt.Errorf("读取key长度失败: %v", err)
	}
	keyLength := binary.LittleEndian.Uint32(keyLengthBytes)

	keyData := make([]byte, keyLength)
	_, err = io.ReadFull(f, keyData)
	if err != nil {
		return result, fmt.Errorf("读取key数据失败: %v", err)
	}

	for i := range keyData {
		keyData[i] ^= 0x64
	}

	keyData, err = aesECBDecrypt(coreKey, keyData)
	if err != nil {
		return result, fmt.Errorf("解密key数据失败: %v", err)
	}

	keyData = pkcs7Unpad(keyData)
	if len(keyData) > 17 {
		keyData = keyData[17:]
	}

	metaLengthBytes := make([]byte, 4)
	_, err = io.ReadFull(f, metaLengthBytes)
	if err != nil {
		return result, fmt.Errorf("读取元数据长度失败: %v", err)
	}
	metaLength := binary.LittleEndian.Uint32(metaLengthBytes)

	var metadata NCMMetadata

	if metaLength > 0 {
		metaData := make([]byte, metaLength)
		_, err = io.ReadFull(f, metaData)
		if err != nil {
			return result, fmt.Errorf("读取元数据失败: %v", err)
		}

		for i := range metaData {
			metaData[i] ^= 0x63
		}

		if len(metaData) > 22 {
			metaData = metaData[22:]
			decodedMeta := make([]byte, base64.StdEncoding.DecodedLen(len(metaData)))
			n, err := base64.StdEncoding.Decode(decodedMeta, metaData)
			if err != nil {
				return result, fmt.Errorf("Base64解码元数据失败: %v", err)
			}
			metaData = decodedMeta[:n]

			metaData, err = aesECBDecrypt(metaKey, metaData)
			if err != nil {
				return result, fmt.Errorf("解密元数据失败: %v", err)
			}

			metaData = pkcs7Unpad(metaData)
			if len(metaData) > 6 {
				metaData = metaData[6:]
			}

			err = json.Unmarshal(metaData, &metadata)
			if err != nil {
				return result, fmt.Errorf("解析元数据JSON失败: %v", err)
			}

			if metadata.Format != "" {
				result.Format = metadata.Format
			}
			if metadata.AlbumPic != "" {
				result.CoverURL = metadata.AlbumPic
			}
		}
	}

	_, err = f.Seek(4, io.SeekCurrent)
	if err != nil {
		return result, fmt.Errorf("跳过CRC32失败: %v", err)
	}

	_, err = f.Seek(5, io.SeekCurrent)
	if err != nil {
		return result, fmt.Errorf("跳过字节失败: %v", err)
	}

	imageSizeBytes := make([]byte, 4)
	_, err = io.ReadFull(f, imageSizeBytes)
	if err != nil {
		return result, fmt.Errorf("读取图片大小失败: %v", err)
	}
	imageSize := binary.LittleEndian.Uint32(imageSizeBytes)

	if imageSize > 0 {
		result.CoverStatus = models.CoverStatusBuiltIn
		result.HasCover = true
	} else if metadata.AlbumPic != "" {
		result.CoverStatus = models.CoverStatusWaitingDownload
		result.HasCover = true
	} else {
		result.CoverStatus = models.CoverStatusNotSupported
		result.HasCover = false
	}

	return result, nil
}
