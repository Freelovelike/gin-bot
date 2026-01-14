package models

import (
	"time"

	"gorm.io/gorm"
)

// User 用户表 (users) —— 基础属性
type User struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	QQ        string         `gorm:"uniqueIndex;not null" json:"qq"`
	Nickname  string         `json:"nickname"`
	Gold      int64          `gorm:"default:0" json:"gold"`
	LastSign  *time.Time     `json:"last_sign"`
	Persona   string         `json:"persona"` // AI 总结的人设摘要
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	ChatHistories []ChatHistory `json:"chat_histories,omitempty"`
}

// ChatHistory 原始消息表 (chat_histories) —— 短期记忆
type ChatHistory struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	UserID    uint           `gorm:"index" json:"user_id"`
	GroupID   int64          `gorm:"index" json:"group_id"`
	Content   string         `gorm:"type:text" json:"content"`
	CreatedAt time.Time      `json:"created_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	User User `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// MemberEmbedding 向量记忆表 (member_embeddings) —— 长期记忆 (RAG 核心)
// 注意：实际向量存储在 Pinecone，此处仅存储 VectorID 作为关联
// 使用 NVIDIA llama-3_2-nemoretriever-300m-embed-v2 模型 (1024 维)
type MemberEmbedding struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	VectorID       string    `gorm:"index" json:"vector_id"`           // Pinecone 中的向量 ID
	ContentSummary string    `gorm:"type:text" json:"content_summary"` // 切片后的文本
	RefMsgID       uint      `gorm:"index" json:"ref_msg_id"`          // 关联到原始消息表
	CreatedAt      time.Time `json:"created_at"`

	RefMsg ChatHistory `gorm:"foreignKey:RefMsgID" json:"ref_msg,omitempty"`
}

// Group 群组配置表 (groups) —— 环境感知
type Group struct {
	GroupID    int64          `gorm:"primaryKey" json:"group_id"`
	IsActive   bool           `gorm:"default:true" json:"is_active"`
	RAGEnabled bool           `gorm:"default:true" json:"rag_enabled"`
	Config     string         `gorm:"type:jsonb" json:"config"` // JSONB 类型
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `gorm:"index" json:"-"`
}
