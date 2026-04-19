package claw

import (
	"time"

	"github.com/goccy/go-json"

	"github.com/opentreehole/go-common"

	. "treehole_next/models"

	"github.com/gofiber/contrib/websocket"
	"github.com/gofiber/fiber/v2"
	"github.com/rs/zerolog/log"
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

	client.IsAuthed = true
	client.UserID = user.ID
	client.ChannelCount = 0

	resp := AuthSuccessMessage{
		Type:         MessageTypeAuthSuccess,
		Timestamp:    time.Now().UnixMilli(),
		ChannelCount: client.UserID, // 这里暂时用UserID模拟ChannelCount，实际业务中应替换为正确的值
		Version:      "1.0",
	}

	if err := c.WriteJSON(resp); err != nil {
		log.Err(err).Msgf("[Claw] Write auth_success error: %v", err)
	}
}

// handleMessage 处理业务消息
func handleMessage(c *websocket.Conn, client *Client, rawMsg json.RawMessage) {
	var msg ClawMessage
	if err := json.Unmarshal(rawMsg, &msg); err != nil {
		sendError(c, ErrCodeUnknownType, "消息格式错误", "", 0)
		return
	}

	// 校验必填字段
	if msg.Content == "" {
		sendError(c, ErrCodeEmptyContent, "消息内容不能为空", msg.MessageID, msg.ChannelID)
		return
	}

	if msg.MessageID == "" {
		sendError(c, ErrCodeUnknownType, "消息ID不能为空", "", msg.ChannelID)
		return
	}

	//TO DO: 实际业务逻辑
	// 暂时直接返回 hello world
	resp := ClawMessage{
		Type:      MessageTypeMessage,
		From:      "server",
		Content:   "hello world",
		MessageID: msg.MessageID, // 回传客户端的消息ID
		ChannelID: msg.ChannelID,
		Timestamp: time.Now().UnixMilli(),
		Media:     Media{},
		Version:   "1.0",
	}

	if err := c.WriteJSON(resp); err != nil {
		log.Err(err).Msgf("[Claw] Write message error: %v", err)
		sendError(c, ErrCodeInternal, "发送消息失败", msg.MessageID, msg.ChannelID)
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
