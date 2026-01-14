package pinecone

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/pinecone-io/go-pinecone/pinecone"
)

var (
	PCClient *pinecone.Client
	PCIndex  *pinecone.IndexConnection
)

// InitPinecone 初始化 Pinecone 客户端
func InitPinecone() {
	apiKey := os.Getenv("PINECONE_API_KEY")
	if apiKey == "" {
		// 使用用户提供的 Key 作为默认值
		apiKey = "pcsk_6LPFmv_QtayH2cAfVq7ZVsozuTyeYfe7PeQimCnordjVSvwJLMUY4xrc4kzQj4cH8KtWRU"
	}

	indexName := os.Getenv("PINECONE_INDEX")
	if indexName == "" {
		indexName = "gin-bot" // 默认索引名
	}

	var err error
	PCClient, err = pinecone.NewClient(pinecone.NewClientParams{
		ApiKey: apiKey,
	})
	if err != nil {
		log.Fatalf("Failed to create Pinecone client: %v", err)
	}

	// 获取索引 Host (IndexConnection 需要 Host)
	ctx := context.Background()
	idx, err := PCClient.DescribeIndex(ctx, indexName)
	if err != nil {
		fmt.Printf("Warning: Failed to describe Pinecone index '%s': %v\n", indexName, err)
		fmt.Println("如果你还未创建索引，请前往 Pinecone 控制台创建一个名为 'rag-bot' 的 Serverless 索引。")
		return
	}

	// 建立索引连接
	PCIndex, err = PCClient.Index(pinecone.NewIndexConnParams{
		Host: idx.Host,
	})
	if err != nil {
		fmt.Printf("Warning: Failed to connect to Pinecone index host '%s': %v\n", idx.Host, err)
	} else {
		fmt.Printf("Connected to Pinecone index: %s (Host: %s)\n", indexName, idx.Host)
	}
}

// Upsert 将向量上传到 Pinecone
func Upsert(ctx context.Context, id string, values []float32, metadata map[string]interface{}) error {
	if PCIndex == nil {
		return fmt.Errorf("pinecone index not connected")
	}

	_, err := PCIndex.UpsertVectors(ctx, []*pinecone.Vector{
		{
			Id:     id,
			Values: values,
		},
	})
	return err
}

// Query 在 Pinecone 中进行向量搜索
func Query(ctx context.Context, vector []float32, topK uint32) ([]string, error) {
	if PCIndex == nil {
		return nil, fmt.Errorf("pinecone index not connected")
	}

	resp, err := PCIndex.QueryByVectorValues(ctx, &pinecone.QueryByVectorValuesRequest{
		Vector: vector,
		TopK:   topK,
	})
	if err != nil {
		return nil, err
	}

	var ids []string
	for _, match := range resp.Matches {
		if match.Vector != nil {
			ids = append(ids, match.Vector.Id)
		}
	}
	return ids, nil
}
