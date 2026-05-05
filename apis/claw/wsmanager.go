package claw

import (
    "sync"
    "time"

    "github.com/gofiber/contrib/websocket"
)

// Client 表示一个已连接的WebSocket客户端
type Client struct {
    Conn         *websocket.Conn
    UserID       int
    IsAuthed     bool
    ChannelCount int        // 该用户已有的对话数
    mu           sync.Mutex // 保护Conn写入
    LastPong     int64      // 最近一次收到客户端 Pong 的时间（毫秒）
}

// Manager WebSocket连接管理器，管理所有活跃连接
// Manager WebSocket连接管理器，管理所有活跃连接，并按 userID 建立索引便于快速查找
type Manager struct {
    clients       map[*websocket.Conn]*Client
    clientsByUser map[int]map[*websocket.Conn]*Client
    mu            sync.RWMutex
    pingInterval  time.Duration
}

var (
    mgrInstance  *Manager
    mgrInitOnce  sync.Once
)

// GetManager 获取Manager单例
func GetManager() *Manager {
    mgrInitOnce.Do(func() {
        mgrInstance = &Manager{
            clients:       make(map[*websocket.Conn]*Client),
            clientsByUser: make(map[int]map[*websocket.Conn]*Client),
            pingInterval:  3 * time.Minute,
        }
        go mgrInstance.startPingLoop()
    })
    return mgrInstance
}

// AddClient 添加新客户端连接
func (m *Manager) AddClient(conn *websocket.Conn, client *Client) {
    m.mu.Lock()
    defer m.mu.Unlock()
    m.clients[conn] = client
    client.LastPong = time.Now().UnixMilli()
}

// RemoveClient 移除客户端连接
func (m *Manager) RemoveClient(conn *websocket.Conn) {
    m.mu.Lock()
    defer m.mu.Unlock()
    cl, ok := m.clients[conn]
    if !ok {
        return
    }
    // 从 user 索引中移除
    if cl.UserID != 0 {
        if ucmap, ok := m.clientsByUser[cl.UserID]; ok {
            delete(ucmap, conn)
            if len(ucmap) == 0 {
                delete(m.clientsByUser, cl.UserID)
            }
        }
    }
    delete(m.clients, conn)
}

// GetClient 获取指定连接的客户端信息
func (m *Manager) GetClient(conn *websocket.Conn) (*Client, bool) {
    m.mu.RLock()
    defer m.mu.RUnlock()
    client, ok := m.clients[conn]
    return client, ok
}

// RegisterUser 在客户端完成认证后调用，用于把连接加入按 userID 的索引
func (m *Manager) RegisterUser(conn *websocket.Conn, userID int) {
    m.mu.Lock()
    defer m.mu.Unlock()
    client, ok := m.clients[conn]
    if !ok {
        return
    }
    client.UserID = userID
    client.IsAuthed = true
    ucmap, ok := m.clientsByUser[userID]
    if !ok {
        ucmap = make(map[*websocket.Conn]*Client)
        m.clientsByUser[userID] = ucmap
    }
    ucmap[conn] = client
}

// GetClientsByUserID 返回指定 userID 的所有客户端
func (m *Manager) GetClientsByUserID(userID int) []*Client {
    m.mu.RLock()
    defer m.mu.RUnlock()
    res := make([]*Client, 0)
    if ucmap, ok := m.clientsByUser[userID]; ok {
        for _, c := range ucmap {
            res = append(res, c)
        }
    }
    return res
}

// GetClientCount 获取当前连接数
func (m *Manager) GetClientCount() int {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return len(m.clients)
}

// startPingLoop 定期向所有已认证客户端发送ping
func (m *Manager) startPingLoop() {
    ticker := time.NewTicker(m.pingInterval)
    defer ticker.Stop()

    for range ticker.C {
        // 复制一份已认证的客户端列表，避免长时间持锁
        m.mu.RLock()
        clients := make([]*Client, 0, len(m.clients))
        for _, client := range m.clients {
            if client.IsAuthed {
                clients = append(clients, client)
            }
        }
        m.mu.RUnlock()

        // 逐个发送ping并检查客户端是否在阈值内有响应（LastPong）
        now := time.Now().UnixMilli()
        staleThreshold := int64(m.pingInterval*2/time.Millisecond) // 两个 pingInterval
        for _, client := range clients {
            ping := PingMessage{
                Type:      MessageTypePing,
                Timestamp: now,
                Version:   "1.0",
            }
            client.mu.Lock()
            err := client.Conn.WriteJSON(ping)
            client.mu.Unlock()

            if err != nil {
                // 发送失败，关闭连接并移除
                client.Conn.Close()
                m.RemoveClient(client.Conn)
                continue
            }

            // 检查最后一次收到 pong 的时间
            client.mu.Lock()
            last := client.LastPong
            client.mu.Unlock()
            if last == 0 {
                // 如果没有收到过 pong，则使用连接添加时间（已在 AddClient 初始化）
                last = now
            }
            if now-last > staleThreshold {
                // 认为客户端掉线，主动断开并移除
                client.Conn.Close()
                m.RemoveClient(client.Conn)
            }
        }
    }
}