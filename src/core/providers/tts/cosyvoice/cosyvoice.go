package cosyvoice

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"xiaozhi-server-go/src/core/providers/tts"

	"xiaozhi-server-go/src/log"
)

// Provider Edge TTS提供者实现
type Provider struct {
	*tts.BaseProvider
}

// NewProvider 创建Edge TTS提供者
func NewProvider(config *tts.Config, deleteFile bool) (*Provider, error) {
	base := tts.NewBaseProvider(config, deleteFile)
	return &Provider{
		BaseProvider: base,
	}, nil
}

type TtsInfo struct {
	Text    string `json:"text"`
	Speaker string `json:"speaker"`
}

func (p *Provider) ToTTS2(text string) (string, error) {
	// 创建临时文件路径用于保存 edgeTTS 生成的 MP3
	outputDir := p.BaseProvider.Config().OutputDir
	if outputDir == "" {
		outputDir = os.TempDir() // Use system temp dir if not configured
	}
	//	curl -sX POST http://192.168.7.169:5000/tts \
	//	  -H "Content-Type: application/json" \
	//	  -d '{"text": "我是通义生成式语音大模型,我说话好听不？", "speaker": "中文女"}' \
	//	  --output output.wav
	url := "http://192.168.8.187:5000/tts"
	tts := TtsInfo{
		Text:    text,
		Speaker: "中文女",
	}
	ttsjs, _ := json.Marshal(tts)
	resp, erx := http.Post(url, "application/json", strings.NewReader(string(ttsjs)))
	if erx != nil {
		log.Errorf("PoserServer error: %v", erx)
		return "", fmt.Errorf("edge-tts-go 获取音频流失败: %v", erx)
	}
	// defer resp.Body.Close()
	body, er2 := io.ReadAll(resp.Body)
	if er2 != nil {
		log.Errorf("PostServer error: %v", er2)
		return "", fmt.Errorf("edge-tts-go 获取音频流失败: %v", er2)
	}
	log.Infof("post_response size: %v", len(body))
	resp.Body.Close()

	tempwav := filepath.Join(outputDir, fmt.Sprintf("cosvoice_%v.wav", time.Now().Format("2006-01-02_15_04_05.000000")))

	// 将音频数据写入临时文件
	err := os.WriteFile(tempwav, body, 0644)
	if err != nil {
		return "", fmt.Errorf("写入音频文件 '%s' 失败: %v", tempwav, err)
	}

	// 检查文件是否成功创建
	if _, err := os.Stat(tempwav); os.IsNotExist(err) {
		return "", fmt.Errorf("edge-tts-go 未能创建音频文件: %s", tempwav)
	}
	//fmt.Printf("音频文件已生成: %s\n", tempFile)

	tempFile := filepath.Join(outputDir, fmt.Sprintf("cosvoice_%v.mp3", time.Now().Format("2006-01-02_15_04_05.000000")))

	cmd2 := exec.Command("lame", "--resample", "24", tempwav, tempFile, "--quiet")
	if err := cmd2.Run(); err != nil {
		return "", fmt.Errorf("执行 lame 命令失败：%v-> %v", err, cmd2.Args)
	}

	// Return the path to the generated audio file
	return tempFile, nil

	// return "", fmt.Errorf("NOT IMPLEMENTED")
}

// ToTTS 将文本转换为音频文件，并返回文件路径
// 使用的edge库是github.com/wujunwei928/edge-tts-go，默认使用24k采样率
func (p *Provider) ToTTS(text string) (string, error) {
	// 获取配置的声音，如果未配置则使用默认值
	// edgeTTSStartTime := time.Now()
	voice := p.BaseProvider.Config().Voice
	if voice == "" {
		voice = "zh-CN-XiaoxiaoNeural" // 默认声音
	}

	// 创建临时文件路径用于保存 edgeTTS 生成的 MP3
	outputDir := p.BaseProvider.Config().OutputDir
	if outputDir == "" {
		outputDir = os.TempDir() // Use system temp dir if not configured
	}
	// Ensure output directory exists
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败 '%s': %v", outputDir, err)
	}
	// Use a unique filename
	tempFile := filepath.Join(outputDir, fmt.Sprintf("macos_say_%v.mp3", time.Now().Format("2006-01-02_15_04_05.000000")))
	tempaiff := filepath.Join(outputDir, fmt.Sprintf("macos_say_%v.aiff", time.Now().Format("2006-01-02_15_04_05.000000")))
	// log.Warnf("====================aiff %v", tempaiff)
	// say "你好，这是一个示例文本。" -o output.aiff && ffmpeg -i output.aiff -codec:a libmp3lame output.mp3
	// cmd := exec.Command("say", string(text), "-o", text+".aiff", " && ", "ffmpeg", "-i", text+".aiff", "-codec:a", "libmp3lame", tempFile)
	cmd := exec.Command("say", string(text), "-o", tempaiff) //meijia //, "-v", "yue"
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("执行 say 命令失败: %v", err)
	}
	// cmd2 := exec.Command("ffmpeg", "-i", tempaiff, "-codec:a", " libmp3lame ", tempFile)
	// Corrected argument "-m", "m" and error message
	cmd2 := exec.Command("lame", "--resample", "24", tempaiff, tempFile, "--quiet")
	if err := cmd2.Run(); err != nil {
		return "", fmt.Errorf("执行 lame 命令失败：%v-> %v", err, cmd2.Args)
	}

	// log.Warnf("====================mp3 %v", tempFile)
	// remove tempaiff
	if err := os.Remove(tempaiff); err != nil {
		log.Errorf("删除临时文件失败: %v", err)
	}
	return tempFile, nil

	/*
		// 配置 edge-tts-go 连接选项
		// connOptions := []edge_tts.CommunicateOption{
		// 	edge_tts.SetVoice(voice),
		// }

		// 创建 Communicate 实例
		// conn, err := edge_tts.NewCommunicate(text, connOptions...)
		// if err != nil {
		// 	return "", fmt.Errorf("创建 edge-tts-go Communicate 失败: %v", err)
		// }

		// curl -s -X POST -d "U盘未挂载" http://yong.yogorobot.com:8080/tts | mpv -
		url := "http://yong.yogorobot.com:8080/tts"
		resp, erx := http.Post(url, "application/text", strings.NewReader(text))
		if erx != nil {
			log.Errorf("PoserServer error:", erx)
			return "", fmt.Errorf("edge-tts-go 获取音频流失败: %v", erx)
		}
		// defer resp.Body.Close()
		body, er2 := io.ReadAll(resp.Body)
		if er2 != nil {
			log.Errorf("PostServer error:", er2)
			return "", fmt.Errorf("edge-tts-go 获取音频流失败: %v", er2)
		}
		log.Infoff("post_response size: %v", len(body))
		resp.Body.Close()
		// 获取音频流数据
		// audioData, err := conn.Stream()
		// if err != nil {
		// 	return "", fmt.Errorf("edge-tts-go 获取音频流失败: %v", err)
		// }

		ttsDuration := time.Since(edgeTTSStartTime)
		_ = ttsDuration
		//fmt.Println(fmt.Sprintf("edge-tts-go 语音合成完成，耗时: %s", ttsDuration))

		// 将音频数据写入临时文件
		err := os.WriteFile(tempFile, body, 0644)
		if err != nil {
			return "", fmt.Errorf("写入音频文件 '%s' 失败: %v", tempFile, err)
		}

		// 检查文件是否成功创建
		if _, err := os.Stat(tempFile); os.IsNotExist(err) {
			return "", fmt.Errorf("edge-tts-go 未能创建音频文件: %s", tempFile)
		}
		//fmt.Printf("音频文件已生成: %s\n", tempFile)

		// Return the path to the generated audio file
		return tempFile, nil
	*/
}

func init() {
	// 注册cosyvoice TTS提供者
	tts.Register("cosyvoice", func(config *tts.Config, deleteFile bool) (tts.Provider, error) {
		return NewProvider(config, deleteFile)
	})
}
