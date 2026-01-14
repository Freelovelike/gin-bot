package service

import (
	"context"
	"fmt"
	"log"
	"time"

	"gin-bot/database"
	"gin-bot/embedding"
	"gin-bot/models"
	"gin-bot/pinecone"
)

// SaveMessageToRAG 将消息存入 RAG 系统
func SaveMessageToRAG(qq string, nickname string, groupID int64, content string) {
	// 1. 记录原始消息到数据库
	// 先找到或创建用户
	var user models.User
	database.DB.FirstOrCreate(&user, models.User{QQ: qq})
	if user.Nickname != nickname {
		user.Nickname = nickname
		database.DB.Save(&user)
	}

	history := models.ChatHistory{
		UserID:  user.ID,
		GroupID: groupID,
		Content: content,
	}
	if err := database.DB.Create(&history).Error; err != nil {
		log.Printf("[RAG] Failed to save chat history: %v", err)
		return
	}

	// 2. 异步生成向量并存入 Pinecone (为了不阻塞主流程，虽然这里已经是后台运行)
	go func() {
		// 生成向量 (使用 passage 模式)
		// NVIDIA 原生 2048 维，截断为 1024 以匹配 Pinecone 索引
		vec, err := embedding.GetEmbedding(content, "passage", 1024)
		if err != nil {
			log.Printf("[RAG] Failed to get embedding for msg %d: %v", history.ID, err)
			return
		}

		// 存入 Pinecone
		vectorID := fmt.Sprintf("msg_%d", history.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		err = pinecone.Upsert(ctx, vectorID, vec, nil)
		if err != nil {
			log.Printf("[RAG] Failed to upsert to Pinecone: %v", err)
			return
		}

		// 3. 记录向量关联到数据库
		embRecord := models.MemberEmbedding{
			VectorID:       vectorID,
			ContentSummary: content, // 这里可以做切片，目前存全文
			RefMsgID:       history.ID,
		}
		database.DB.Create(&embRecord)
		
		log.Printf("[RAG] Successfully archived message %d from %s", history.ID, nickname)
	}()
}
