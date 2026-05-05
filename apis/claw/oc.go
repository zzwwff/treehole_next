package claw

import (
    "sync"
    "time"
    "strings"
    "strconv"

    "github.com/goccy/go-json"
    "github.com/gofiber/contrib/websocket"
    "github.com/rs/zerolog/log"

    "github.com/opentreehole/go-common"

    . "treehole_next/models"
)

// OcClient 单一 OpenClaw 客户端（全局唯一，不使用连接池）
type OcClient struct {
    Conn     *websocket.Conn
    UserID   int
    IsAuthed bool
    mu       sync.Mutex
}

var (
    ocClient   *OcClient
    ocClientMu sync.Mutex
)

// HandleOpenClawWebSocket 处理 /claw/oc 的 WebSocket 连接（全局仅允许一个连接）
func HandleOpenClawWebSocket(c *websocket.Conn) {
    client := &OcClient{
        Conn:     c,
        IsAuthed: false,
    }

    // 定期向 OpenClaw 网关连接发送心跳（每 30 秒）
    pingDone := make(chan struct{})
    go func() {
        ticker := time.NewTicker(30 * time.Second)
        defer ticker.Stop()
        for {
            select {
            case <-ticker.C:
                ping := PingMessage{
                    Type:      MessageTypePing,
                    Timestamp: time.Now().UnixMilli(),
                    Version:   "1.0",
                }
                client.mu.Lock()
                err := c.WriteJSON(ping)
                client.mu.Unlock()
                if err != nil {
                    log.Err(err).Msg("[Claw-OC] send ping failed; closing oc connection")
                    // 发送失败，主动关闭连接以触发上层清理
                    _ = c.Close()
                    return
                }
            case <-pingDone:
                return
            }
        }
    }()

    defer func() {
        // 清理全局引用（如果是当前连接）
        ocClientMu.Lock()
        if ocClient == client {
            ocClient = nil
        }
        ocClientMu.Unlock()
        // 停止 ping 协程并关闭连接
        close(pingDone)
        c.Close()
    }()

    var rawMsg json.RawMessage
    for {
        err := c.ReadJSON(&rawMsg)
        if err != nil {
            log.Err(err).Msg("[Claw-OC] Read error")
            break
        }

        var base BaseMessage
        if err := json.Unmarshal(rawMsg, &base); err != nil {
            sendOcError(c, ErrCodeUnknownType, "消息格式错误", "", "")
            continue
        }
        log.Info().Msgf("[Claw-OC] recv type=%s raw=%s", base.Type, string(rawMsg))

        switch base.Type {
        case MessageTypeAuth:
            handleOcAuth(c, client, rawMsg)
        case MessageTypeMessage:
            if !client.IsAuthed {
                sendOcError(c, ErrCodeNotAuthed, "请先完成认证", "", "")
                continue
            }
            handleOcMessage(c, client, rawMsg)
        case MessageTypePing:
            var ping PingMessage
            if err := json.Unmarshal(rawMsg, &ping); err == nil {
                pong := PongMessage{
                    Type:      MessageTypePong,
                    Timestamp: time.Now().UnixMilli(),
                    Version:   "1.0",
                }
                client.mu.Lock()
                _ = c.WriteJSON(pong)
                client.mu.Unlock()
            }
        default:
            sendOcError(c, ErrCodeUnknownType, "未知的消息类型", "", "")
        }
    }
}

func handleOcAuth(c *websocket.Conn, client *OcClient, raw json.RawMessage) {
    var authMsg AuthMessage
    if err := json.Unmarshal(raw, &authMsg); err != nil {
        sendOcError(c, ErrCodeAuthFailed, "认证消息格式错误", "", "")
        return
    }

    if authMsg.Token == "" {
        sendOcError(c, ErrCodeAuthFailed, "token不能为空", "", "")
        return
    }

    user := &User{BanDivision: make(map[int]*time.Time)}
    if err := common.ParseJWTToken(authMsg.Token, user); err != nil {
        sendOcError(c, ErrCodeAuthFailed, "token 解析失败，请重新登录", "", "")
        return
    }
    if user.ID == 0 {
        sendOcError(c, ErrCodeAuthFailed, "token 中未包含合法用户信息", "", "")
        return
    }

    if err := user.LoadUserByID(user.ID); err != nil {
        log.Err(err).Msg("[Claw-OC] load user failed")
        sendOcError(c, ErrCodeAuthFailed, "认证失败，请稍后重试", "", "")
        return
    }

    // 在认证阶段拒绝已有已认证连接
    ocClientMu.Lock()
    if ocClient != nil && ocClient.IsAuthed {
        ocClientMu.Unlock()
        sendOcError(c, ErrCodeAuthFailed, "已有 OpenClaw 网关连接，拒绝重复连接", "", "")
        // 直接关闭新连接
        _ = c.Close()
        return
    }
    // 将当前连接设置为全局客户端（标记为已认证后写回 auth_success）
    ocClient = client
    ocClientMu.Unlock()

    client.IsAuthed = true
    client.UserID = user.ID

    resp := AuthSuccessMessage{
        Type:      MessageTypeAuthSuccess,
        Timestamp: time.Now().UnixMilli(),
        Version:   "1.0",
    }
    client.mu.Lock()
    _ = c.WriteJSON(resp)
    client.mu.Unlock()
    log.Info().Msgf("[Claw-OC] auth_success userID=%d", user.ID)
}

func handleOcMessage(c *websocket.Conn, client *OcClient, raw json.RawMessage) {
    var msg ClawMessage
    if err := json.Unmarshal(raw, &msg); err != nil {
        sendOcError(c, ErrCodeUnknownType, "消息格式错误", "", "")
        return
    }

    // 对于 OpenClaw 协议，必须使用 task_id 字段来关联请求/响应
    if msg.TaskID == "" {
        sendOcError(c, ErrCodeUnknownType, "task_id 不能为空", "", msg.SessionID)
        return
    }

    // 首先尝试通过 task_id 解析出 userID 和 channelID，格式：task_<userID>_<channelID>_...。
    parts := strings.Split(msg.TaskID, "_")
    var targetUserID int
    var targetChannelID int
    parsed := false
    if len(parts) >= 3 {
        if uid, err := strconv.Atoi(parts[1]); err == nil {
            if cid, err2 := strconv.Atoi(parts[2]); err2 == nil {
                targetUserID = uid
                targetChannelID = cid
                parsed = true
            }
        }
    }

    // 持久化消息到数据库
	msg.From = "openclaw"
    msg.ChannelID = targetChannelID
    msg.Timestamp = time.Now().UnixMilli()
    msg.Version = "1.0"
    if err := CreateMessage(DB, &msg); err != nil {
        log.Err(err).Msg("[Claw-OC] create message failed")
        sendOcError(c, ErrCodeInternal, "保存消息失败", msg.TaskID, msg.SessionID)
        return
    }

    // 转发到对应用户的前端客户端（如果已连接），发送给前端时去掉 session_id 字段
    if parsed {
        wsMgr := GetManager()
        clients := wsMgr.GetClientsByUserID(targetUserID)
        if len(clients) == 0 {
            log.Info().Msgf("[Claw-OC] frontend client for user %d not connected; message saved only", targetUserID)
        } else {
            for _, clientWS := range clients {
                forward := msg
                forward.SessionID = ""
                clientWS.mu.Lock()
                err := clientWS.Conn.WriteJSON(forward)
                clientWS.mu.Unlock()
                if err != nil {
                    log.Err(err).Msgf("[Claw-OC] forward to frontend user %d failed", targetUserID)
                }
            }
        }
    } else {
        // 无法从 task_id 解析 user 时，不尝试路由
        log.Info().Msg("[Claw-OC] no task-based routing available; message saved only")
    }
}

func sendOcError(c *websocket.Conn, code string, errMsg string, taskID string, sessionID string) {
    // 包含 task_id 和可选的 session_id，方便 OpenClaw 定位
    payload := map[string]interface{}{
        "type":          MessageTypeError,
        "code":          code,
        "error_message": errMsg,
        "timestamp":     time.Now().UnixMilli(),
    }
    if taskID != "" {
        payload["task_id"] = taskID
    }
    if sessionID != "" {
        payload["session_id"] = sessionID
    }
    _ = c.WriteJSON(payload)
}
