package claw

import (
	"fmt"
	"time"

	"github.com/goccy/go-json"

	"github.com/opentreehole/go-common"

	. "treehole_next/models"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"
)

// clawtest
//
// @Summary Temporary Test for openclaw
// @Tags claw
// @Accept application/json
// @Produce application/json
// @Router /claw/test [post]
// @Param json body OpenClawTest true "json"
// @Failure 400 {object} MessageModel
func clawtest(c *fiber.Ctx) error {
	// validate body
	var body OpenClawTest
	err := common.ValidateBody(c, &body)
	if err != nil {
		return err
	}

	// get user
	user, err := GetCurrLoginUser(c)
	if err != nil {
		return err
	}

	// permission check
	if !user.IsAdmin {
		return common.Forbidden()
	}

	return common.BadRequest("The path forward is leaved for further exploration.")
}

// ListChannels
//
// @Summary List Users' all channels
// @Tags Claw
// @Produce application/json
// @Router /claw/channels [get]
// @Success 200 {array} ClawSession
func ListChannels(c *fiber.Ctx) error {
	// get user
	user, err := GetCurrLoginUser(c)
	if err != nil {
		return err
	}

	sessions, err := GetSessionsByUserID(DB, user.ID)
	if err != nil {
		log.Err(err).Msg("[Claw] get sessions failed")
		return common.BadRequest("获取对话列表失败")
	}
	for _, session := range sessions {
		(*session).OC_SessionID = ""
		(*session).ID = 0
		(*session).UserID = 0
		(*session).Conversation = ""
	}
	return c.JSON(sessions)
}

// ListMessages
//
// @Summary List Users' all messages in a specific channel
// @Tags Claw
// @Produce application/json
// @Router /claw/messages [get]
// @Param object query ListClawMessageModel false "query"
// @Success 200 {array} ClawMessage
func ListMessages(c *fiber.Ctx) error {
	// get user
	user, err := GetCurrLoginUser(c)
	if err != nil {
		return err
	}
	var query ListClawMessageModel
	err = common.ValidateQuery(c, &query)
	if err != nil {
		return err
	}

	// 校验频道归属
	_, err = GetSessionByUserAndSessionID(DB, user.ID, query.ChannelID)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return common.NotFound("会话不存在")
		}
		log.Err(err).Msg("[Claw] get session failed")
		return common.BadRequest("获取会话信息失败")
	}

	// 获取消息列表
	if query.Size == 0 {
		query.Size = 30
	}
	messages, err := GetMessagesByChannelID(DB, query.ChannelID, query.Size, query.Offset, query.Sort)
	if err != nil {
		log.Err(err).Msg("[Claw] get messages failed")
		return common.BadRequest("获取消息列表失败")
	}

	return c.JSON(messages)
}

// HandleWebSocket WebSocket连接主处理函数
func HandleWebSocket(c *websocket.Conn) {
	mgr := GetManager()

	// 初始化客户端，此时未认证
	client := &Client{
		Conn:     c,
		IsAuthed: false,
	}
	mgr.AddClient(c, client)

	// 确保退出时清理
	defer func() {
		mgr.RemoveClient(c)
		c.Close()
	}()

	// 消息读取循环
	var rawMsg json.RawMessage
	for {
		err := c.ReadJSON(&rawMsg)
		if err != nil {
			// 读取错误通常是连接断开
			log.Err(err).Msg("[Claw] WebSocket read error")
			break
		}

		// 先解析消息类型
		var base BaseMessage
		if err := json.Unmarshal(rawMsg, &base); err != nil {
			sendError(c, ErrCodeUnknownType, "消息格式错误", "", 0)
			continue
		}
		log.Info().Msgf("[Claw] WS recv type=%s raw=%s", base.Type, string(rawMsg))

		// 根据类型路由到不同处理函数
		switch base.Type {
		case MessageTypeAuth:
			handleAuth(c, client, rawMsg)
		case MessageTypeMessage:
			if !client.IsAuthed {
				sendError(c, ErrCodeNotAuthed, "请先完成认证", "", 0)
				continue
			}
			handleMessage(c, client, rawMsg)
		case MessageTypePong:
			// 收到pong，无需处理，保持连接即可
		default:
			sendError(c, ErrCodeUnknownType, "未知的消息类型", "", 0)
		}
	}
}

// handleAuth 处理认证请求
func handleAuth(c *websocket.Conn, client *Client, rawMsg json.RawMessage) {
	var authMsg AuthMessage
	if err := json.Unmarshal(rawMsg, &authMsg); err != nil {
		sendError(c, ErrCodeAuthFailed, "认证消息格式错误", "", 0)
		return
	}
	log.Info().Msgf("[Claw] auth received token_len=%d", len(authMsg.Token))

	if authMsg.Token == "" {
		sendError(c, ErrCodeAuthFailed, "token不能为空", "", 0)
		return
	}

	// 解析并校验 JWT token
	user := &User{BanDivision: make(map[int]*time.Time)}
	if err := common.ParseJWTToken(authMsg.Token, user); err != nil {
		sendError(c, ErrCodeAuthFailed, "token 解析失败，请重新登录", "", 0)
		return
	}

	if user.ID == 0 {
		sendError(c, ErrCodeAuthFailed, "token 中未包含合法用户信息", "", 0)
		return
	}

	// 从数据库加载用户完整信息
	if err := user.LoadUserByID(user.ID); err != nil {
		log.Err(err).Msg("[Claw] load user failed")
		sendError(c, ErrCodeAuthFailed, "认证失败，请稍后重试", "", 0)
		return
	}

	count, err := GetSessionCountByUserID(DB, user.ID)
	if err != nil {
		log.Err(err).Msg("[Claw] count sessions failed")
		sendError(c, ErrCodeAuthFailed, "认证失败，请稍后重试", "", 0)
		return
	}

	client.IsAuthed = true
	client.UserID = user.ID
	client.ChannelCount = int(count)

	resp := AuthSuccessMessage{
		Type:         MessageTypeAuthSuccess,
		Timestamp:    time.Now().UnixMilli(),
		ChannelCount: int(count),
		Version:      "1.0",
	}

	if err := c.WriteJSON(resp); err != nil {
		log.Err(err).Msgf("[Claw] Write auth_success error: %v", err)
		return
	}
	log.Info().Msgf("[Claw] auth_success sent userID=%d channelCount=%d", user.ID, count)
}

// handleMessage 处理业务消息
func handleMessage(c *websocket.Conn, client *Client, rawMsg json.RawMessage) {
	var msg ClawMessage
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		sendError(c, ErrCodeUnknownType, "消息格式错误", "", 0)
		return
	}
	log.Info().Msgf("[Claw] message received messageID=%s channelID=%d content_len=%d", msg.MessageID, msg.ChannelID, len(msg.Content))

	// 校验必填字段
	if msg.Content == "" {
		sendError(c, ErrCodeEmptyContent, "消息内容不能为空", msg.MessageID, msg.ChannelID)
		return
	}

	if msg.MessageID == "" {
		sendError(c, ErrCodeUnknownType, "消息ID不能为空", "", msg.ChannelID)
		return
	}

	var channelID int
	if msg.ChannelID == 0 {
		// 创建新会话
		ocSessionID := fmt.Sprintf("oc-%d-%d", client.UserID, time.Now().UnixMilli())
		session, err := CreateSession(DB, client.UserID, "新会话", ocSessionID)
		if err != nil {
			log.Err(err).Msg("[Claw] create session failed")
			sendError(c, ErrCodeInternal, "创建会话失败", msg.MessageID, 0)
			return
		}
		channelID = session.UserSessionID
	} else {
		// 检查会话是否存在
		_, err := GetSessionByUserAndSessionID(DB, client.UserID, msg.ChannelID)
		if err != nil {
			if err == gorm.ErrRecordNotFound {
				sendError(c, ErrCodeUnknownType, "会话不存在", msg.MessageID, msg.ChannelID)
			} else {
				log.Err(err).Msg("[Claw] get session failed")
				sendError(c, ErrCodeInternal, "查询会话失败", msg.MessageID, msg.ChannelID)
			}
			return
		}
		channelID = msg.ChannelID
	}

	// 保存用户消息到数据库
	msg.From = fmt.Sprintf("user")
	msg.ChannelID = channelID
	msg.Timestamp = time.Now().UnixMilli()
	msg.Version = "1.0"
	if err := CreateMessage(DB, &msg); err != nil {
		log.Err(err).Msg("[Claw] create message failed")
		sendError(c, ErrCodeInternal, "保存消息失败", msg.MessageID, channelID)
		return
	}

	// 返回用户消息
	if err := c.WriteJSON(msg); err != nil {
		log.Err(err).Msgf("[Claw] Write user message error: %v", err)
		sendError(c, ErrCodeInternal, "发送消息失败", msg.MessageID, channelID)
		return
	}

	// 生成 hello world 回复并落库
	replyMsg := ClawMessage{
		Type:      MessageTypeMessage,
		From:      "assistant",
		Content:   "hello world",
		MessageID: fmt.Sprintf("reply-%d", time.Now().UnixMilli()),
		ChannelID: channelID,
		Timestamp: time.Now().UnixMilli(),
		Version:   "1.0",
	}
	if err := CreateMessage(DB, &replyMsg); err != nil {
		log.Err(err).Msg("[Claw] create reply message failed")
		sendError(c, ErrCodeInternal, "保存回复失败", replyMsg.MessageID, channelID)
		return
	}

	// 发回复给客户端
	if err := c.WriteJSON(replyMsg); err != nil {
		log.Err(err).Msgf("[Claw] Write reply message error: %v", err)
		sendError(c, ErrCodeInternal, "发送回复失败", replyMsg.MessageID, channelID)
	}
}

// sendError 发送错误消息给客户端
func sendError(c *websocket.Conn, code string, errMsg string, messageID string, channelID int) {
	resp := ErrorMessage{
		Type:      MessageTypeError,
		Code:      code,
		ErrorMsg:  errMsg,
		MessageID: messageID,
		ChannelID: channelID,
		Timestamp: time.Now().UnixMilli(),
	}

	if err := c.WriteJSON(resp); err != nil {
		log.Err(err).Msgf("[Claw] Write error message failed: %v", err)
	}
}
