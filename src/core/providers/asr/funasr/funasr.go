package funasr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"xiaozhi-server-go/src/core/providers/asr"
	"xiaozhi-server-go/src/core/utils"

	"github.com/gorilla/websocket"
)

// 超时设置
const (
	idleTimeout = 30 * time.Second // 没有新数据就结束识别
)

// Ensure Provider implements asr.Provider interface
var _ asr.Provider = (*Provider)(nil)

// Provider FunASR提供者实现
type Provider struct {
	*asr.BaseProvider
	outputDir string
	host      string
	wsURL     string
	connectID string
	logger    *utils.Logger

	// FunASR配置
	asrMode   string
	chunkSize []int
	wavFormat string
	audioFs   int
	useItn    bool
	hotwords  map[string]int

	// 流式识别相关字段
	conn        *websocket.Conn
	isStreaming bool
	reqID       string
	result      string
	err         error
	connMutex   sync.Mutex

	sendDataCnt int
}

// NewProvider 创建FunASR提供者实例
func NewProvider(config *asr.Config, deleteFile bool, logger *utils.Logger) (*Provider, error) {
	base := asr.NewBaseProvider(config, deleteFile)

	// 从config.Data中获取配置
	outputDir, _ := config.Data["output_dir"].(string)
	if outputDir == "" {
		outputDir = "tmp/"
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("创建输出目录失败: %v", err)
	}

	// 创建连接ID
	connectID := fmt.Sprintf("%d", time.Now().UnixNano())

	// 默认配置
	asrMode := "2pass-online"
	chunkSize := []int{1, 2, 1} // 调整为更小的chunk_size以减少延迟
	wavFormat := "pcm"
	audioFs := 16000
	useItn := true
	hotwords := make(map[string]int)

	// 从config中获取可选配置
	if mode, ok := config.Data["asr_mode"].(string); ok {
		asrMode = mode
	}
	if chunks, ok := config.Data["chunk_size"].([]interface{}); ok {
		chunkSize = make([]int, len(chunks))
		for i, v := range chunks {
			if val, ok := v.(float64); ok {
				chunkSize[i] = int(val)
			}
		}
	}
	if format, ok := config.Data["wav_format"].(string); ok {
		wavFormat = format
	}
	if fs, ok := config.Data["audio_fs"].(float64); ok {
		audioFs = int(fs)
	}
	if itn, ok := config.Data["use_itn"].(bool); ok {
		useItn = itn
	}
	if hws, ok := config.Data["hotwords"].(map[string]interface{}); ok {
		for k, v := range hws {
			if val, ok := v.(float64); ok {
				hotwords[k] = int(val)
			}
		}
	}

	provider := &Provider{
		BaseProvider: base,
		outputDir:    outputDir,
		host:         "10.43.254.18",
		wsURL:        "ws://10.43.254.18:10096/",
		connectID:    connectID,
		logger:       logger,

		asrMode:   asrMode,
		chunkSize: chunkSize,
		wavFormat: wavFormat,
		audioFs:   audioFs,
		useItn:    useItn,
		hotwords:  hotwords,
	}

	// 初始化音频处理
	provider.InitAudioProcessing()

	return provider, nil
}

// 读取根目录下的mp3文件，测试Transcribe方法
func (p *Provider) TestTranscribe() (string, error) {
	fmt.Println("TestTranscribe called")
	// 读取音频文件
	audioFile := "700.mp3" // 替换为实际的音频文件路径

	pcmData, err := utils.MP3ToPCMData(audioFile)
	if err != nil {
		fmt.Println("MP3转PCM失败: ", err.Error())
	}
	monoPcmDataBytes := []byte{}
	if len(pcmData) > 0 {
		monoPcmDataBytes = pcmData[0] // 提取第一个切片
		fmt.Printf("提取的单声道PCM数据长度: %d 字节\n", len(monoPcmDataBytes))

	} else {
		fmt.Println("没有PCM数据可提取")
	}

	result, err := p.Transcribe(context.Background(), monoPcmDataBytes)
	if err != nil {
		fmt.Println("转录失败: ", err.Error())
	} else {
		fmt.Print("result is ", result, "\n")
	}

	return result, nil
}

// Transcribe 实现asr.Provider接口的转录方法
func (p *Provider) Transcribe(ctx context.Context, audioData []byte) (string, error) {
	if p.isStreaming {
		return "", fmt.Errorf("正在进行流式识别, 请先调用Reset")
	}

	// 创建临时文件
	tempFile := filepath.Join(p.outputDir, fmt.Sprintf("temp_%d.wav", time.Now().UnixNano()))
	if err := os.WriteFile(tempFile, audioData, 0644); err != nil {
		return "", fmt.Errorf("保存临时文件失败: %v", err)
	}
	defer func() {
		if p.DeleteFile() {
			os.Remove(tempFile)
		}
	}()

	// 初始化连接
	if err := p.Initialize(); err != nil {
		return "", err
	}
	defer p.Cleanup()

	// 添加音频数据
	if err := p.AddAudioWithContext(ctx, audioData); err != nil {
		return "", err
	}
	// 等待结果,无法立即返回正确的结果，通过回调函数返回
	return p.result, nil
}

// validateAudioFormat 验证音频数据格式
func (p *Provider) validateAudioFormat(data []byte) error {
	if len(data) == 0 {
		return fmt.Errorf("音频数据为空")
	}

	// 检查是否是16位PCM数据的基本特征
	// 16位PCM数据应该是偶数长度（每个样本2字节）
	if len(data)%2 != 0 {
		p.logger.Warn("[WARN] 音频数据长度不是偶数，可能不是16位PCM格式: 长度=%d", len(data))
	}

	// 计算理论上的样本数
	// sampleCount := len(data) / 2
	// durationSeconds := float64(sampleCount) / float64(p.audioFs)

	// p.logger.Info("[DEBUG] 音频格式验证: 长度=%d字节, 样本数=%d, 理论时长=%.2f秒, 采样率=%dHz",
	// 	len(data), sampleCount, durationSeconds, p.audioFs)

	// 检查是否有静音或异常数据
	silenceCount := 0
	maxValue := 0
	for i := 0; i < len(data); i += 2 {
		if i+1 >= len(data) {
			break
		}
		sample := int16(data[i]) | (int16(data[i+1]) << 8)
		if sample == 0 {
			silenceCount++
		}
		absVal := int(sample)
		if absVal < 0 {
			absVal = -absVal
		}
		if absVal > maxValue {
			maxValue = absVal
		}
	}

	// silenceRatio := float64(silenceCount) / float64(sampleCount)
	// p.logger.Info("[DEBUG] 音频数据统计: 静音样本比例=%.2f%%, 最大振幅=%d", silenceRatio*100, maxValue)

	// if silenceRatio > 0.95 {
	// 	p.logger.Warn("[WARN] 音频数据几乎全部是静音，可能麦克风未工作或音量太低")
	// }

	return nil
}

// constructRequest 构造FunASR初始请求
func (p *Provider) constructRequest() map[string]interface{} {
	request := map[string]interface{}{
		"mode":        p.asrMode,
		"chunk_size":  p.chunkSize,
		"wav_name":    p.reqID,
		"wav_format":  p.wavFormat,
		"audio_fs":    p.audioFs,
		"is_speaking": true,
		"itn":         p.useItn,
	}

	if len(p.hotwords) > 0 {
		hotwordsJson, _ := json.Marshal(p.hotwords)
		request["hotwords"] = string(hotwordsJson)
	}

	return request
}

// parseResponse 解析FunASR响应数据
func (p *Provider) parseResponse(data []byte) (map[string]interface{}, error) {
	var jsonData map[string]interface{}
	if err := json.Unmarshal(data, &jsonData); err != nil {
		return nil, fmt.Errorf("解析JSON响应失败: %v", err)
	}
	p.logger.Debug("[DEBUG] parseResponse: JSON解析成功, 数据=%v", jsonData)
	return jsonData, nil
}

// AddAudio 添加音频数据到缓冲区
func (p *Provider) AddAudio(data []byte) error {
	return p.AddAudioWithContext(context.Background(), data)
}

// AddAudioWithContext 带上下文的音频数据添加
func (p *Provider) AddAudioWithContext(ctx context.Context, data []byte) error {
	// 使用锁检查状态
	p.connMutex.Lock()
	isStreaming := p.isStreaming
	p.connMutex.Unlock()

	if !isStreaming {
		err := p.StartStreaming(ctx)
		if err != nil {
			return err
		}
	}

	// 检查是否有实际数据需要发送
	if len(data) > 0 && p.isStreaming {
		// 验证音频格式
		if err := p.validateAudioFormat(data); err != nil {
			p.logger.Error("音频格式验证失败: %v", err)
			return err
		}

		// 记录音频数据信息用于调试
		// p.logger.Info("[DEBUG] AddAudioWithContext: 准备发送音频数据, 长度=%d 字节, 采样率=%dHz, 格式=%s", len(data), p.audioFs, p.wavFormat)
		// if len(data) >= 44 { // 检查是否可能是WAV格式
		// 	p.logger.Debug("[DEBUG] 音频数据前44字节: %x", data[:44])
		// }

		// 直接发送音频数据
		if err := p.sendAudioData(data, false); err != nil {
			return err
		} else {
			p.sendDataCnt += 1
			if p.sendDataCnt%20 == 0 {
				p.logger.Debug("发送音频数据成功, 长度: %d 字节", len(data))
			}
		}
	}

	return nil
}

func (p *Provider) StartStreaming(ctx context.Context) error {
	p.logger.Info("----开始FunASR流式识别----")
	p.ResetStartListenTime()
	// 加锁保护连接初始化
	p.connMutex.Lock()
	defer p.connMutex.Unlock()

	// 双重检查，避免并发初始化
	if p.isStreaming {
		return nil
	}

	// 初始化流式识别
	p.InitAudioProcessing()
	p.result = ""
	p.err = nil

	// 确保旧连接已关闭
	if p.conn != nil {
		p.closeConnection()
	}

	// 建立WebSocket连接
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	// 重试机制
	var conn *websocket.Conn
	var resp *http.Response
	var err error
	maxRetries := 2

	for i := 0; i <= maxRetries; i++ {
		conn, resp, err = dialer.DialContext(ctx, p.wsURL, nil) // FunASR不需要headers
		if err == nil {
			break
		}

		if i < maxRetries {
			backoffTime := time.Duration(500*(i+1)) * time.Millisecond
			p.logger.Warn("WebSocket连接失败(尝试%d/%d): %v, 将在%v后重试", i+1, maxRetries+1, err, backoffTime)
			time.Sleep(backoffTime)
		}
	}

	if err != nil {
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
		}
		return fmt.Errorf("WebSocket连接失败(状态码:%d): %v", statusCode, err)
	}

	p.conn = conn

	// 发送初始请求
	p.reqID = fmt.Sprintf("%d", time.Now().UnixNano())
	request := p.constructRequest()
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("构造请求数据失败: %v", err)
	}

	p.logger.Info("[DEBUG] 发送FunASR初始请求: %s", string(requestBytes))

	// 发送JSON请求
	if err := p.conn.WriteMessage(websocket.TextMessage, requestBytes); err != nil {
		return fmt.Errorf("发送请求失败: %v", err)
	}

	p.isStreaming = true
	p.logger.Debug("[DEBUG] FunASR流式识别初始化成功, connectID=%s, reqID=%s", p.connectID, p.reqID)

	// 开启一个协程来处理响应
	go func() {
		p.ReadMessage()
	}()
	return nil
}

func (p *Provider) ReadMessage() {
	p.logger.Info("FunASR流式识别协程已启动")
	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("FunASR流式识别协程发生错误: %v", r)
		}
		p.connMutex.Lock()
		p.isStreaming = false
		if p.conn != nil {
			p.logger.Info("ReadMessage协程结束，关闭WebSocket连接")
			p.closeConnection()
		}
		p.connMutex.Unlock()
		p.logger.Info("FunASR流式识别协程已结束")
	}()

	for {
		// 检查连接状态，避免在连接关闭后继续读取
		p.connMutex.Lock()
		if !p.isStreaming || p.conn == nil {
			p.connMutex.Unlock()
			p.logger.Info("FunASR流式识别已结束或连接已关闭，退出读取循环")
			return
		}
		conn := p.conn
		p.connMutex.Unlock()

		conn.SetReadDeadline(time.Now().Add(30 * time.Second))

		messageType, response, err := conn.ReadMessage()
		if err != nil {
			// 检查是否是连接断开相关的错误
			errMsg := err.Error()
			if strings.Contains(errMsg, "close 1006") ||
				strings.Contains(errMsg, "abnormal closure") ||
				strings.Contains(errMsg, "unexpected EOF") {
				p.logger.Info("检测到服务端主动断开连接: %v", err)
			}
			p.setErrorAndStop(err)
			return
		}

		if messageType != websocket.TextMessage {
			continue // 只处理文本消息
		}

		result, err := p.parseResponse(response)
		if err != nil {
			p.setErrorAndStop(fmt.Errorf("解析响应失败: %v", err))
			return
		}

		// 记录所有接收到的响应用于调试
		p.logger.Info("[DEBUG] 接收到FunASR响应: %s", string(response))

		// 检查是否为最终结果
		if isFinal, ok := result["is_final"].(bool); ok && isFinal {
			p.logger.Info("FunASR识别完成 (is_final=true)")
		}

		isPassOffline := false
		if result["mode"].(string) == "2pass-offline" {
			isPassOffline = true
			p.logger.Info("FunASR识别完成 2pass-offline")
		}

		// 提取文本结果
		text := ""
		if textData, hasText := result["text"].(string); hasText && isPassOffline {
			text = textData
		}

		if text != "" {
			p.logger.Info("FunASR识别结果: '%s'", text)
		} else {
			p.logger.Debug("[DEBUG] 响应中无文本内容")
		}

		// 在流式识别中，只有在is_final=true时才结束识别
		// 其他情况下继续监听，除非上层明确要求结束
		shouldFinish := false

		if listener := p.BaseProvider.GetListener(); listener != nil {
			if text != "" {
				// 有文本结果时重置静音计数和开始时间
				p.BaseProvider.SilenceCount = 0
				p.ResetStartListenTime()

				// 调用listener，但只有在is_final=true时才根据返回值决定是否结束
				if isFinal, ok := result["is_final"].(bool); ok && isFinal {
					if finished := listener.OnAsrResult(text); finished {
						shouldFinish = true
					}
				} else {
					// 非最终结果，只通知但不结束
					listener.OnAsrResult(text)
				}
			} else {
				// 没有文本结果时，检查是否长时间静音
				if p.SilenceTime() > idleTimeout {
					p.BaseProvider.SilenceCount += 1
					if p.BaseProvider.SilenceCount >= 3 { // 连续3次静音
						p.logger.Info("检测到长时间静音，结束识别")
						text = "你没有听清我说话"
						listener.OnAsrResult(text)
						shouldFinish = true
					}
				}
			}
		}

		if shouldFinish {
			return
		}
	}
}
func (p *Provider) setErrorAndStop(err error) {
	p.logger.Warn("FunASR发生错误，停止识别: %v", err)
	p.connMutex.Lock()
	defer p.connMutex.Unlock()

	// 避免重复设置错误状态
	if !p.isStreaming {
		return
	}

	p.err = err
	p.isStreaming = false
	errMsg := err.Error()
	if strings.Contains(errMsg, "use of closed network connection") ||
		strings.Contains(errMsg, "close 1006") ||
		strings.Contains(errMsg, "abnormal closure") {
		p.logger.Debug("检测到连接断开: %v, sendDataCnt=%d", err, p.sendDataCnt)
	} else {
		p.logger.Error("其他WebSocket错误: %v, sendDataCnt=%d", err, p.sendDataCnt)
	}

	if p.conn != nil {
		p.closeConnection()
	}
}

func (p *Provider) closeConnection() {
	defer func() {
		if r := recover(); r != nil {
			// 静默处理panic，避免程序崩溃
			p.logger.Error("关闭连接时发生错误: %v", r)
		}
	}()

	if p.conn != nil {
		// 不发送关闭消息，直接关闭连接
		_ = p.conn.Close()
		p.conn = nil
	}
}

// sendAudioData 发送音频数据
func (p *Provider) sendAudioData(data []byte, isLast bool) error {
	p.logger.Debug("[DEBUG] sendAudioData: 数据长度=%d, isLast=%t, sendDataCnt=%d", len(data), isLast, p.sendDataCnt)

	defer func() {
		if r := recover(); r != nil {
			p.logger.Error("发送音频数据时发生panic: %v", r)
		}
	}()

	if p.conn == nil {
		return fmt.Errorf("WebSocket连接不存在")
	}

	// 分块发送音频数据，参考C++客户端的实现
	blockSize := 102400 // 102400字节 ≈ 3.2秒的16位PCM数据
	offset := 0
	totalLen := len(data)

	for offset < totalLen {
		sendBlock := blockSize
		if offset+sendBlock > totalLen {
			sendBlock = totalLen - offset
		}

		// 发送数据块
		if err := p.conn.WriteMessage(websocket.BinaryMessage, data[offset:offset+sendBlock]); err != nil {
			return fmt.Errorf("发送音频数据块失败 (offset=%d, size=%d): %v", offset, sendBlock, err)
		}

		p.logger.Debug("[DEBUG] 发送音频数据块: offset=%d, size=%d 字节", offset, sendBlock)
		offset += sendBlock
	}

	p.logger.Debug("[DEBUG] 音频数据发送完成，总大小: %d 字节", totalLen)
	return nil
}

// Reset 重置ASR状态
func (p *Provider) Reset() error {
	p.logger.Info("开始重置FunASR状态")
	// 使用锁保护状态变更
	p.connMutex.Lock()
	defer p.connMutex.Unlock()

	// 先设置isStreaming为false，让ReadMessage协程退出
	p.isStreaming = false

	// 发送结束消息（如果连接仍然有效）
	if p.conn != nil {
		endMsg := map[string]interface{}{
			"is_speaking": false,
		}
		endBytes, _ := json.Marshal(endMsg)
		p.logger.Info("[DEBUG] 发送FunASR结束消息: %s", string(endBytes))

		// 使用goroutine和超时来避免阻塞和并发问题
		done := make(chan error, 1)
		go func() {
			done <- p.conn.WriteMessage(websocket.TextMessage, endBytes)
		}()

		select {
		case err := <-done:
			if err != nil {
				p.logger.Debug("发送结束消息失败（连接可能已断开）: %v", err)
			} else {
				time.Sleep(100 * time.Millisecond) // 等待消息发送完成
			}
		case <-time.After(500 * time.Millisecond):
			p.logger.Debug("发送结束消息超时，跳过")
		}
	}

	p.closeConnection()

	p.reqID = ""
	p.result = ""
	p.err = nil

	// 重置音频处理
	p.InitAudioProcessing()

	p.logger.Info("FunASR状态已重置")

	return nil
}

// Initialize 实现Provider接口的Initialize方法
func (p *Provider) Initialize() error {
	// 确保输出目录存在
	if err := os.MkdirAll(p.outputDir, 0755); err != nil {
		return fmt.Errorf("初始化输出目录失败: %v", err)
	}
	return nil
}

// Cleanup 实现Provider接口的Cleanup方法
func (p *Provider) Cleanup() error {
	// 使用锁保护状态变更
	p.connMutex.Lock()
	defer p.connMutex.Unlock()

	// 确保WebSocket连接关闭
	p.closeConnection()

	p.logger.Info("ASR资源已清理")

	return nil
}

func init() {
	// funasr注册ASR提供者
	asr.Register("funasr", func(config *asr.Config, deleteFile bool, logger *utils.Logger) (asr.Provider, error) {
		return NewProvider(config, deleteFile, logger)
	})
}
