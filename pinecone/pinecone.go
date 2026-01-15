package pinecone

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/pinecone-io/go-pinecone/pinecone"
	"google.golang.org/protobuf/types/known/structpb"
)

// Namespace 常量定义
const (
	NamespacePersonal = "personal" // 个人信息命名空间
	NamespaceChat     = "chat"     // 群聊命名空间
)

// Match 包含查询结果的 ID 和分数
type Match struct {
	ID    string
	Score float32
}

var (
	PCClient  *pinecone.Client
	PCIndex   *pinecone.IndexConnection
	indexHost string // 保存 Host 用于创建带 namespace 的连接
)

// InitPinecone 初始化 Pinecone 客户端
func InitPinecone() {
	apiKey := os.Getenv("PINECONE_API_KEY")
	if apiKey == "" {
		apiKey = "pcsk_6LPFmv_QtayH2cAfVq7ZVsozuTyeYfe7PeQimCnordjVSvwJLMUY4xrc4kzQj4cH8KtWRU"
	}

	indexName := os.Getenv("PINECONE_INDEX")
	if indexName == "" {
		indexName = "gin-bot"
	}

	var err error
	PCClient, err = pinecone.NewClient(pinecone.NewClientParams{
		ApiKey: apiKey,
	})
	if err != nil {
		log.Fatalf("Failed to create Pinecone client: %v", err)
	}

	ctx := context.Background()
	idx, err := PCClient.DescribeIndex(ctx, indexName)
	if err != nil {
		fmt.Printf("Warning: Failed to describe Pinecone index '%s': %v\n", indexName, err)
		return
	}

	indexHost = idx.Host

	// 默认连接（无 namespace）
	PCIndex, err = PCClient.Index(pinecone.NewIndexConnParams{
		Host: indexHost,
	})
	if err != nil {
		fmt.Printf("Warning: Failed to connect to Pinecone index: %v\n", err)
	} else {
		fmt.Printf("Connected to Pinecone index: %s (Host: %s)\n", indexName, indexHost)
	}
}

// getIndexWithNamespace 获取指定 namespace 的索引连接
func getIndexWithNamespace(namespace string) (*pinecone.IndexConnection, error) {
	if PCClient == nil || indexHost == "" {
		return nil, fmt.Errorf("pinecone not initialized")
	}
	return PCClient.Index(pinecone.NewIndexConnParams{
		Host:      indexHost,
		Namespace: namespace,
	})
}

// UpsertToNamespace 将向量上传到指定 namespace
func UpsertToNamespace(ctx context.Context, namespace, id string, values []float32, metadata map[string]interface{}) error {
	idx, err := getIndexWithNamespace(namespace)
	if err != nil {
		return err
	}

	vec := &pinecone.Vector{
		Id:     id,
		Values: values,
	}

	if len(metadata) > 0 {
		metaStruct, err := structpb.NewStruct(metadata)
		if err != nil {
			log.Printf("[Pinecone] Failed to create metadata struct: %v", err)
		} else {
			vec.Metadata = metaStruct
		}
	}

	_, err = idx.UpsertVectors(ctx, []*pinecone.Vector{vec})
	return err
}

// QueryFromNamespace 从指定 namespace 查询向量
func QueryFromNamespace(ctx context.Context, namespace string, vector []float32, topK uint32, filter map[string]interface{}) ([]string, error) {
	matches, err := QueryWithScore(ctx, namespace, vector, topK, filter)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, m := range matches {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// QueryWithScore 从指定 namespace 查询向量并返回分数
func QueryWithScore(ctx context.Context, namespace string, vector []float32, topK uint32, filter map[string]interface{}) ([]Match, error) {
	idx, err := getIndexWithNamespace(namespace)
	if err != nil {
		return nil, err
	}

	req := &pinecone.QueryByVectorValuesRequest{
		Vector: vector,
		TopK:   topK,
	}

	if len(filter) > 0 {
		filterStruct, err := structpb.NewStruct(filter)
		if err != nil {
			log.Printf("[Pinecone] Failed to create filter struct: %v", err)
		} else {
			req.MetadataFilter = filterStruct
		}
	}

	resp, err := idx.QueryByVectorValues(ctx, req)
	if err != nil {
		return nil, err
	}

	var matches []Match
	for _, m := range resp.Matches {
		if m.Vector != nil {
			matches = append(matches, Match{
				ID:    m.Vector.Id,
				Score: m.Score,
			})
		}
	}
	return matches, nil
}

// Upsert 兼容旧接口（默认 namespace）
// func Upsert(ctx context.Context, id string, values []float32, metadata map[string]interface{}) error {
// 	return UpsertToNamespace(ctx, "", id, values, metadata)
// }

// Query 兼容旧接口
// func Query(ctx context.Context, vector []float32, topK uint32) ([]string, error) {
// 	return QueryFromNamespace(ctx, "", vector, topK, nil)
// }
