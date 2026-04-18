package models

import (
	"time"

	"gorm.io/gorm"
)

// ClawSession OpenClaw 会话表
type ClawSession struct {
	ID           uint      `json:"id" gorm:"primaryKey"`
	UserID       int       `json:"user_id" gorm:"index"`
	Conversation string    `json:"conversation"`                     // 用户自定义的会话名称
	OC_SessionID string    `json:"oc_session_id" gorm:"uniqueIndex"` // OpenClaw 生成的 session ID
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (ClawSession) TableName() string {
	return "claw_session"
}

// GetSessionsByUserID 获取用户的所有会话
func GetSessionsByUserID(tx *gorm.DB, userID int) ([]ClawSession, error) {
	data := make([]ClawSession, 0)
	err := tx.Where("user_id = ?", userID).Order("created_at DESC").Find(&data).Error
	return data, err
}

// GetSessionByOCID 通过 OpenClaw SessionID 查询
func GetSessionByOCID(tx *gorm.DB, ocSessionID string) (*ClawSession, error) {
	var session ClawSession
	err := tx.Where("oc_session_id = ?", ocSessionID).First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// CreateSession 创建新会话
func CreateSession(tx *gorm.DB, userID int, conversation string, ocSessionID string) (*ClawSession, error) {
	session := &ClawSession{
		UserID:       userID,
		Conversation: conversation,
		OC_SessionID: ocSessionID,
	}
	err := tx.Create(session).Error
	return session, err
}

// UpdateSession 更新会话名称
func UpdateSession(tx *gorm.DB, id int, conversation string) error {
	return tx.Model(&ClawSession{}).Where("id = ?", id).
		Updates(map[string]interface{}{
			"conversation": conversation,
			"updated_at":   time.Now(),
		}).Error
}

// DeleteSession 删除会话
func DeleteSession(tx *gorm.DB, id int) error {
	return tx.Where("id = ?", id).Delete(&ClawSession{}).Error
}

// GetOrCreateSession 获取或创建会话（你说的"不存在则新建"）
func GetOrCreateSession(tx *gorm.DB, userID int, conversation string, ocSessionID string) (*ClawSession, error) {
	// 先查是否存在
	var session ClawSession
	err := tx.Where("user_id = ? AND oc_session_id = ?", userID, ocSessionID).First(&session).Error
	if err == nil {
		return &session, nil // 已存在，直接返回
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err // 其他错误
	}
	// 不存在则创建
	return CreateSession(tx, userID, conversation, ocSessionID)
}
