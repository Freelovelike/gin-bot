package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
)

const (
	NVIDIA_API_URL = "https://integrate.api.nvidia.com/v1/embeddings"
	NVIDIA_MODEL   = "nvidia/llama-3.2-nemoretriever-300m-embed-v2"
)

type EmbeddingRequest struct {
	Input     []string `json:"input"`
	Model     string   `json:"model"`
	InputType string   `json:"input_type"` // "query" or "passage"
	Encoding  string   `json:"encoding_format"`
}

type EmbeddingData struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
	Object    string    `json:"object"`
}

type EmbeddingResponse struct {
	Object string          `json:"object"`
	Data   []EmbeddingData `json:"data"`
	Model  string          `json:"model"`
	Usage  struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// GetEmbedding 调用 NVIDIA API 获取文本向量
// inputType: "query" 用于检索，"passage" 用于建立索引
// targetDim: 目标维度，如果为 0 则返回原始维度（该模型默认为 2048）
func GetEmbedding(text string, inputType string, targetDim int) ([]float32, error) {
	apiKey := os.Getenv("NVIDIA_API_KEY")
	if apiKey == "" {
		apiKey = "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"
	}

	reqBody := EmbeddingRequest{
		Input:     []string{text},
		Model:     NVIDIA_MODEL,
		InputType: inputType,
		Encoding:  "float",
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %v", err)
	}

	req, err := http.NewRequest("POST", NVIDIA_API_URL, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// 配置代理
	proxyUrl, _ := url.Parse("http://127.0.0.1:7890")
	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyUrl),
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("api error (status %d): %s", resp.StatusCode, string(body))
	}

	var result EmbeddingResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if len(result.Data) == 0 {
		return nil, fmt.Errorf("no embedding data returned")
	}

	embeddings := result.Data[0].Embedding

	// 如果指定了目标维度且小于原始维度，执行截断 (Matryoshka Truncation)
	if targetDim > 0 && len(embeddings) > targetDim {
		return embeddings[:targetDim], nil
	}

	return embeddings, nil
}
