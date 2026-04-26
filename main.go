package main

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
	"time"
)

var musicSuffixList = []string{"mp3", "wav", "ape", "flac"}

type NCMMetadata struct {
	Format   string `json:"format"`
	AlbumPic string `json:"albumPic"`
}

func main() {
	var rootDir string
	if len(os.Args) > 1 {
		rootDir = os.Args[1]
	} else {
		exePath, err := os.Executable()
		if err != nil {
			fmt.Printf("获取可执行文件路径失败: %v\n", err)
			return
		}
		rootDir = filepath.Dir(exePath)
	}

	fmt.Printf(">>>>>>>>>>>>>>> 初始路径层级: %s\n", rootDir)

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

	if err != nil {
		fmt.Printf("遍历目录失败: %v\n", err)
		return
	}

	if len(ncmFiles) == 0 {
		fmt.Println("没有找到NCM文件")
		return
	}

	fmt.Printf("找到 %d 个NCM文件，开始并发处理...\n", len(ncmFiles))
	ProcessFilesConcurrently(ncmFiles)

	fmt.Printf("全部文件处理完成 %s\n", time.Now().Format("2006-01-02 15:04:05"))
}

func ProcessFilesConcurrently(filePaths []string) {
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 10) // 限制并发数为10

	for _, filePath := range filePaths {
		wg.Add(1)
		go func(fp string) {
			defer wg.Done()
			semaphore <- struct{}{}        // 获取信号量
			defer func() { <-semaphore }() // 释放信号量

			fmt.Printf(">>>>>>>>>>>>>>>> 当前文件: %s\n", fp)

			// 检查是否已存在同名的已转换文件
			baseName := strings.TrimSuffix(filepath.Base(fp), filepath.Ext(fp))
			dir := filepath.Dir(fp)
			exists := false

			for _, suffix := range musicSuffixList {
				checkPath := filepath.Join(dir, baseName+"."+suffix)
				if _, err := os.Stat(checkPath); err == nil {
					exists = true
					break
				}
			}

			if exists {
				fmt.Printf(">>>>>>>>>>>>>>> 同名文件跳过: %s\n", fp)
				return
			}

			fmt.Printf(">>>>>>>>>>>>>>> 开始转码文件: %s\n", fp)
			err := dump(fp, baseName)
			if err != nil {
				fmt.Printf("转码文件失败: %s error: %v\n", fp, err)
			} else {
				fmt.Printf(">>>>>>>>>>>>>>> 转码文件成功: %s\n", fp)
			}
		}(filePath)
	}

	wg.Wait()
}

func dump(filePath, fileNameNoSuffix string) error {
	coreKey := []byte{0x68, 0x7A, 0x48, 0x52, 0x41, 0x6D, 0x73, 0x6F, 0x35, 0x6B, 0x49, 0x6E, 0x62, 0x61, 0x78, 0x57}
	metaKey := []byte{0x23, 0x31, 0x34, 0x6C, 0x6A, 0x6B, 0x5F, 0x21, 0x5C, 0x5D, 0x26, 0x30, 0x55, 0x3C, 0x27, 0x28}

	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("无法打开文件: %v", err)
	}
	defer f.Close()

	// 读取并验证头部
	header := make([]byte, 8)
	_, err = io.ReadFull(f, header)
	if err != nil {
		return fmt.Errorf("读取头部失败: %v", err)
	}
	if !bytes.Equal(header, []byte("CTENFDAM")) {
		return fmt.Errorf("不是有效的NCM文件")
	}

	// 跳过2字节
	_, err = f.Seek(2, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("跳过字节失败: %v", err)
	}

	// 读取key长度
	keyLengthBytes := make([]byte, 4)
	_, err = io.ReadFull(f, keyLengthBytes)
	if err != nil {
		return fmt.Errorf("读取key长度失败: %v", err)
	}
	keyLength := binary.LittleEndian.Uint32(keyLengthBytes)

	// 读取并解密key数据
	keyData := make([]byte, keyLength)
	_, err = io.ReadFull(f, keyData)
	if err != nil {
		return fmt.Errorf("读取key数据失败: %v", err)
	}

	for i := range keyData {
		keyData[i] ^= 0x64
	}

	// AES-ECB解密key数据
	keyData, err = aesECBDecrypt(coreKey, keyData)
	if err != nil {
		return fmt.Errorf("解密key数据失败: %v", err)
	}

	// 去除填充并跳过前17字节
	keyData = pkcs7Unpad(keyData)
	if len(keyData) > 17 {
		keyData = keyData[17:]
	}
	keyLength = uint32(len(keyData))

	// 构建key box
	keyBox := make([]byte, 256)
	for i := range keyBox {
		keyBox[i] = byte(i)
	}

	var c uint8 = 0
	var lastByte uint8 = 0
	var keyOffset uint32 = 0

	for i := 0; i < 256; i++ {
		swap := keyBox[i]
		c = (swap + lastByte + keyData[keyOffset]) & 0xff
		keyOffset++
		if keyOffset >= keyLength {
			keyOffset = 0
		}
		keyBox[i] = keyBox[c]
		keyBox[c] = swap
		lastByte = c
	}

	// 读取元数据长度
	metaLengthBytes := make([]byte, 4)
	_, err = io.ReadFull(f, metaLengthBytes)
	if err != nil {
		return fmt.Errorf("读取元数据长度失败: %v", err)
	}
	metaLength := binary.LittleEndian.Uint32(metaLengthBytes)

	// 读取并解密元数据
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

		// Base64解码并跳过前22字节
		if len(metaData) > 22 {
			metaData = metaData[22:]
			decodedMeta := make([]byte, base64.StdEncoding.DecodedLen(len(metaData)))
			n, err := base64.StdEncoding.Decode(decodedMeta, metaData)
			if err != nil {
				return fmt.Errorf("Base64解码元数据失败: %v", err)
			}
			metaData = decodedMeta[:n]

			// AES-ECB解密元数据
			metaData, err = aesECBDecrypt(metaKey, metaData)
			if err != nil {
				return fmt.Errorf("解密元数据失败: %v", err)
			}

			// 去除填充并跳过前6字节
			metaData = pkcs7Unpad(metaData)
			if len(metaData) > 6 {
				metaData = metaData[6:]
			}

			// 解析JSON
			err = json.Unmarshal(metaData, &metadata)
			if err != nil {
				return fmt.Errorf("解析元数据JSON失败: %v", err)
			}
		}
	}

	// 跳过CRC32
	_, err = f.Seek(4, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("跳过CRC32失败: %v", err)
	}

	// 跳过5字节
	_, err = f.Seek(5, io.SeekCurrent)
	if err != nil {
		return fmt.Errorf("跳过字节失败: %v", err)
	}

	// 读取图片大小
	imageSizeBytes := make([]byte, 4)
	_, err = io.ReadFull(f, imageSizeBytes)
	if err != nil {
		return fmt.Errorf("读取图片大小失败: %v", err)
	}
	imageSize := binary.LittleEndian.Uint32(imageSizeBytes)

	// 读取图片数据
	if imageSize > 0 {
		imageData = make([]byte, imageSize)
		_, err = io.ReadFull(f, imageData)
		if err != nil {
			return fmt.Errorf("读取图片数据失败: %v", err)
		}
	}

	// 确定输出文件名
	var outputFileName string
	if metadata.Format != "" {
		outputFileName = fileNameNoSuffix + "." + metadata.Format
	} else {
		// 默认使用mp3格式
		outputFileName = fileNameNoSuffix + ".mp3"
		metadata.Format = "mp3"
	}

	outputFilePath := filepath.Join(filepath.Dir(filePath), outputFileName)

	// 解密音频数据并写入输出文件
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

		// 解密chunk
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

	// 关闭输出文件，确保在嵌入封面之前文件已解锁
	outFile.Close()

	// 嵌入封面
	if len(imageData) > 0 {
		fmt.Printf("使用NCM文件中提取的封面图片，大小: %d 字节\n", len(imageData))
		embedCover(outputFilePath, imageData, metadata.Format)
	} else if metadata.AlbumPic != "" {
		fmt.Println("NCM文件中没有找到封面图片，尝试从网络下载...")
		resp, err := http.Get(metadata.AlbumPic)
		if err == nil && resp.StatusCode == 200 {
			defer resp.Body.Close()
			coverData, err := io.ReadAll(resp.Body)
			if err == nil {
				fmt.Printf("从网络下载封面图片成功，大小: %d 字节\n", len(coverData))
				embedCover(outputFilePath, coverData, metadata.Format)
			}
		} else {
			if err != nil {
				fmt.Printf("从网络下载封面图片出错: %v\n", err)
			} else {
				fmt.Printf("从网络下载封面图片失败，状态码: %d\n", resp.StatusCode)
			}
		}
	}

	return nil
}

func embedCover(audioPath string, coverData []byte, format string) bool {
	if len(coverData) == 0 {
		fmt.Println("封面数据为空，无法嵌入")
		return false
	}

	format = strings.ToLower(format)

	// 检查是否是支持的格式
	supportedFormats := map[string]bool{
		"mp3":  true,
		"flac": true,
		"wav":  true,
		"ape":  true,
	}

	if !supportedFormats[format] {
		fmt.Printf("不支持的音频格式: %s，封面嵌入跳过\n", format)
		return false
	}

	// 检查ffmpeg是否可用
	_, err := exec.LookPath("ffmpeg")
	if err != nil {
		fmt.Printf("未找到ffmpeg，无法嵌入封面。请安装ffmpeg并添加到系统PATH中。\n")
		// 如果ffmpeg不可用，尝试保存封面到文件
		coverPath := audioPath + ".cover.jpg"
		if err := os.WriteFile(coverPath, coverData, 0644); err == nil {
			fmt.Printf("封面图片已保存到: %s\n", coverPath)
			return true
		}
		return false
	}

	// 保存封面数据到临时文件
	tempCoverPath := audioPath + ".temp_cover.jpg"
	if err := os.WriteFile(tempCoverPath, coverData, 0644); err != nil {
		fmt.Printf("保存临时封面文件失败: %v\n", err)
		return false
	}
	defer os.Remove(tempCoverPath) // 清理临时文件

	// 创建临时输出文件
	tempOutputPath := audioPath + ".temp_output" + filepath.Ext(audioPath)

	// 统一的ffmpeg命令，适用于所有四种格式
	cmd := exec.Command("ffmpeg", "-i", audioPath, "-i", tempCoverPath,
		"-map", "0:a", "-map", "1:v", "-c", "copy",
		"-metadata:s:v", "title=Album cover", "-metadata:s:v", "comment=Cover (front)",
		"-disposition:v", "attached_pic", "-y", tempOutputPath)

	// 捕获输出以便调试
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		fmt.Printf("ffmpeg执行失败: %v\n%s\n", err, stderr.String())
		// 如果ffmpeg失败，尝试保存封面到文件
		coverPath := audioPath + ".cover.jpg"
		if err := os.WriteFile(coverPath, coverData, 0644); err == nil {
			fmt.Printf("封面图片已保存到: %s\n", coverPath)
			return true
		}
		return false
	}

	// 替换原文件
	if err := os.Remove(audioPath); err != nil {
		fmt.Printf("删除原文件失败: %v\n", err)
		os.Remove(tempOutputPath)
		return false
	}

	if err := os.Rename(tempOutputPath, audioPath); err != nil {
		fmt.Printf("重命名临时文件失败: %v\n", err)
		return false
	}

	fmt.Printf("封面已嵌入到%s文件: %s\n", strings.ToUpper(format), audioPath)
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
