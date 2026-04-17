package tests

import (
	"testing"

	"github.com/stretchr/testify/assert"
	. "treehole_next/models"
)

func TestCreateSession(t *testing.T) {
	session, err := CreateSession(DB, 1, "测试会话", "oc-123")
	assert.NoError(t, err)
	assert.Equal(t, 1, session.UserID)
	assert.Equal(t, "测试会话", session.Conversation)
	assert.Equal(t, "oc-123", session.OC_SessionID)
}

func TestGetSessionsByUserID(t *testing.T) {
	CreateSession(DB, 2, "会话1", "oc-u2-1")
	CreateSession(DB, 2, "会话2", "oc-u2-2")

	sessions, err := GetSessionsByUserID(DB, 2)
	assert.NoError(t, err)
	assert.Len(t, sessions, 2)
}

func TestGetSessionByOCID(t *testing.T) {
	CreateSession(DB, 1, "测试", "oc-find")

	session, err := GetSessionByOCID(DB, "oc-find")
	assert.NoError(t, err)
	assert.Equal(t, "oc-find", session.OC_SessionID)

	_, err = GetSessionByOCID(DB, "not-exist")
	assert.Error(t, err)
}

func TestUpdateSession(t *testing.T) {
	session, _ := CreateSession(DB, 1, "旧名称", "oc-upd")

	err := UpdateSession(DB, int(session.ID), "新名称")
	assert.NoError(t, err)

	updated, _ := GetSessionByOCID(DB, "oc-upd")
	assert.Equal(t, "新名称", updated.Conversation)
}

func TestDeleteSession(t *testing.T) {
	session, _ := CreateSession(DB, 1, "待删除", "oc-del")

	err := DeleteSession(DB, int(session.ID))
	assert.NoError(t, err)

	_, err = GetSessionByOCID(DB, "oc-del")
	assert.Error(t, err)
}

func TestGetOrCreateSession(t *testing.T) {
	s1, err := GetOrCreateSession(DB, 1, "新会话", "oc-goc")
	assert.NoError(t, err)
	assert.Equal(t, "新会话", s1.Conversation)

	s2, err := GetOrCreateSession(DB, 1, "不同名称", "oc-goc")
	assert.NoError(t, err)
	assert.Equal(t, s1.ID, s2.ID)
}
