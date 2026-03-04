package main

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/yockii/wangshu-manager/internal/config"
	"github.com/yockii/wangshu-manager/internal/process"
)

type Server struct {
	server         *http.Server
	upgrader       websocket.Upgrader
	clients        map[string]*websocket.Conn
	clientsMu      sync.RWMutex
	wangshuPath    string
	cfg            *config.Config
	cfgMu          sync.RWMutex
	processManager *process.ProcessManager
}

func NewServer(cfg *config.Config, wangshuPath string) (*Server, error) {
	addr := cfg.Channels.Web.HostAddress
	if addr == "" {
		addr = ":8080"
	}

	s := &Server{
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
		clients:        make(map[string]*websocket.Conn),
		wangshuPath:    wangshuPath,
		cfg:            cfg,
		processManager: process.NewProcessManager(wangshuPath),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.handlewangshuWebSocket)
	mux.HandleFunc("/webWs", s.handleWebWebSocket)
	mux.HandleFunc("/api/", s.handleAPI)
	mux.HandleFunc("/", s.handleStatic)

	s.server = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	return s, nil
}

func (s *Server) Start() error {
	return s.server.ListenAndServe()
}

func (s *Server) Stop() error {
	slog.Info("wangshu Manager stopping")
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	for _, conn := range s.clients {
		conn.Close()
	}
	return s.server.Close()
}

func (s *Server) handlewangshuWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !s.validateToken(token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade WebSocket", "error", err)
		return
	}

	clientID := "wangshu-" + r.RemoteAddr
	s.clientsMu.Lock()
	s.clients[clientID] = conn
	s.clientsMu.Unlock()

	slog.Info("wangshu connected", "client", clientID)
	s.broadcastWangshuStatus("connected")

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, clientID)
		s.clientsMu.Unlock()
		conn.Close()
		slog.Info("wangshu disconnected", "client", clientID)
		s.broadcastWangshuStatus("disconnected")
	}()

	for {
		var msg struct {
			Type    string `json:"type"`
			Content string `json:"content"`
			Session string `json:"session,omitempty"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			slog.Error("Failed to read WebSocket message", "error", err)
			return
		}

		s.broadcastToClients(msg)
	}
}

func (s *Server) handleWebWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !s.validateToken(token) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("Failed to upgrade web WebSocket", "error", err)
		return
	}

	webClientID := "web-" + r.RemoteAddr
	s.clientsMu.Lock()
	s.clients[webClientID] = conn
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, webClientID)
		s.clientsMu.Unlock()
		slog.Info("Web client disconnected", "client", webClientID)
	}()

	slog.Info("Web client connected", "client", webClientID, "total", len(s.clients))

	// 立即发送wangshu的当前连接状态
	s.clientsMu.RLock()
	wangshuConnected := false
	for clientID := range s.clients {
		if len(clientID) > 7 && clientID[:7] == "wangshu-" {
			wangshuConnected = true
			break
		}
	}
	s.clientsMu.RUnlock()

	status := "disconnected"
	if wangshuConnected {
		status = "connected"
	}

	msg := map[string]interface{}{
		"type":   "wangshu_status",
		"status": status,
	}
	if err := conn.WriteJSON(msg); err != nil {
		slog.Error("Failed to send initial wangshu status", "error", err)
	} else {
		slog.Info("Sent initial wangshu status", "status", status)
	}

	for {
		var msg struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			slog.Error("Web client disconnected", "error", err)
			return
		}

		slog.Info("Received message from web client", "client", webClientID, "type", msg.Type, "content", msg.Content)

		s.clientsMu.RLock()
		wangshuConnected := false
		wangshuCount := 0
		for clientID, wangshuConn := range s.clients {
			if len(clientID) > 7 && clientID[:7] == "wangshu-" {
				wangshuCount++
				if err := wangshuConn.WriteJSON(msg); err != nil {
					slog.Error("Failed to forward message to wangshu", "error", err)
				} else {
					slog.Info("Forwarded message to wangshu", "client", clientID)
					wangshuConnected = true
				}
			}
		}
		slog.Info("Total wangshu connections", "count", wangshuCount)

		broadcastCount := 0
		webClientCount := 0

		// 创建带有role字段的消息用于广播给其他web客户端
		broadcastMsg := map[string]interface{}{
			"type":    msg.Type,
			"content": msg.Content,
			"role":    "user",
		}

		for otherClientID, webConn := range s.clients {
			if len(otherClientID) > 4 && otherClientID[:4] == "web-" {
				webClientCount++
				if otherClientID != webClientID {
					if err := webConn.WriteJSON(broadcastMsg); err != nil {
						slog.Error("Failed to broadcast message to other web client", "client", otherClientID, "error", err)
					} else {
						broadcastCount++
						slog.Debug("Broadcasted message to other web client", "client", otherClientID)
					}
				}
			}
		}
		s.clientsMu.RUnlock()

		slog.Info("Message forwarding summary", "wangshu_count", wangshuCount, "web_client_count", webClientCount, "broadcast_count", broadcastCount)

		if !wangshuConnected {
			slog.Warn("wangshu not connected, message not forwarded")
		}
		if broadcastCount > 0 {
			slog.Info("Broadcasted message to other web clients", "count", broadcastCount)
		}
	}
}

func (s *Server) broadcastToClients(msg interface{}) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	webClientCount := 0
	for clientID, conn := range s.clients {
		// 只向web客户端发送消息
		if len(clientID) > 4 && clientID[:4] == "web-" {
			if err := conn.WriteJSON(msg); err != nil {
				slog.Error("Failed to broadcast message", "error", err)
			} else {
				webClientCount++
				slog.Debug("Sent message to web client", "client", clientID)
			}
		}
	}
	slog.Info("Broadcasted message to web clients", "count", webClientCount)
}

func (s *Server) broadcastWangshuStatus(status string) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()

	msg := map[string]interface{}{
		"type":   "wangshu_status",
		"status": status,
	}

	webClientCount := 0
	for clientID, conn := range s.clients {
		// 只向web客户端发送状态消息，不发送给wangshu
		if len(clientID) > 4 && clientID[:4] == "web-" {
			if err := conn.WriteJSON(msg); err != nil {
				slog.Error("Failed to broadcast wangshu status", "error", err)
			} else {
				webClientCount++
				slog.Debug("Broadcasted wangshu status", "client", clientID, "status", status)
			}
		}
	}
	slog.Info("Broadcasted wangshu status to web clients", "count", webClientCount, "status", status)
}

func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	if !s.authenticate(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	path := r.URL.Path[len("/api/"):]
	switch path {
	case "sessions":
		s.handleSessions(w, r)
	case "tasks":
		s.handleTasks(w, r)
	case "cron":
		s.handleCron(w, r)
	case "config":
		s.handleConfig(w, r)
	case "instance":
		s.handleInstance(w, r)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	fs := http.FileServer(http.Dir("static"))
	fs.ServeHTTP(w, r)
}

func (s *Server) authenticate(r *http.Request) bool {
	token := r.Header.Get("Authorization")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	return s.validateToken(token)
}

func (s *Server) validateToken(token string) bool {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()

	if s.cfg.Channels.Web.Token == "" {
		return true
	}
	return token == s.cfg.Channels.Web.Token
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	agentKey := r.URL.Query().Get("agent")
	if agentKey == "" {
		http.Error(w, "Agent parameter is required", http.StatusBadRequest)
		return
	}

	s.cfgMu.RLock()
	agent, exists := s.cfg.Agents[agentKey]
	s.cfgMu.RUnlock()

	if !exists {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	workspace := agent.Workspace
	sessionsDir := filepath.Join(workspace, "sessions")

	sessions, err := s.loadSessions(sessionsDir)
	if err != nil {
		slog.Error("Failed to load sessions", "error", err)
		http.Error(w, "Failed to load sessions", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"sessions": sessions,
	})
}

type Session struct {
	ChatID   string    `json:"chat_id"`
	Channel  string    `json:"channel"`
	Messages []Message `json:"messages"`
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Timestamp string     `json:"timestamp"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result,omitempty"`
}

func (s *Server) loadSessions(sessionsDir string) ([]Session, error) {
	var sessions []Session

	channelDirs, err := os.ReadDir(sessionsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []Session{}, nil
		}
		return nil, err
	}

	for _, channelDir := range channelDirs {
		if !channelDir.IsDir() {
			continue
		}

		channel := channelDir.Name()
		channelPath := filepath.Join(sessionsDir, channel)

		sessionFiles, err := os.ReadDir(channelPath)
		if err != nil {
			slog.Error("Failed to read channel directory", "channel", channel, "error", err)
			continue
		}

		for _, sessionFile := range sessionFiles {
			if sessionFile.IsDir() || !strings.HasSuffix(sessionFile.Name(), ".jsonl") {
				continue
			}

			chatID := strings.TrimSuffix(sessionFile.Name(), ".jsonl")
			// 处理web下文件名可能为空的情况
			if chatID == "" || chatID == channel {
				chatID = "web"
			}
			sessionFilePath := filepath.Join(channelPath, sessionFile.Name())

			messages, err := s.loadMessages(sessionFilePath)
			if err != nil {
				slog.Error("Failed to load messages", "file", sessionFilePath, "error", err)
				continue
			}

			sessions = append(sessions, Session{
				ChatID:   chatID,
				Channel:  channel,
				Messages: messages,
			})
		}
	}

	return sessions, nil
}

func (s *Server) loadMessages(filePath string) ([]Message, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var messages []Message
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			slog.Error("Failed to parse message", "error", err)
			continue
		}
		messages = append(messages, msg)
	}

	return messages, scanner.Err()
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	agentKey := r.URL.Query().Get("agent")
	if agentKey == "" {
		http.Error(w, "Agent parameter is required", http.StatusBadRequest)
		return
	}

	s.cfgMu.RLock()
	agent, exists := s.cfg.Agents[agentKey]
	s.cfgMu.RUnlock()

	if !exists {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	workspace := agent.Workspace
	tasksDir := filepath.Join(workspace, "tasks")

	tasks, err := s.loadTasks(tasksDir)
	if err != nil {
		slog.Error("Failed to load tasks", "error", err)
		http.Error(w, "Failed to load tasks", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tasks": tasks,
	})
}

type TaskInfo struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Priority    string    `json:"priority"`
	Status      string    `json:"status"`
	LastResult  string    `json:"last_result"`
	Channel     string    `json:"channel"`
	ChatID      string    `json:"chat_id"`
	History     []Message `json:"history,omitempty"`
}

func (s *Server) loadTasks(tasksDir string) ([]TaskInfo, error) {
	var tasks []TaskInfo

	taskDirs, err := os.ReadDir(tasksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []TaskInfo{}, nil
		}
		return nil, err
	}

	for _, taskDir := range taskDirs {
		if !taskDir.IsDir() {
			continue
		}

		taskID := taskDir.Name()
		taskPath := filepath.Join(tasksDir, taskID)

		task, err := s.loadTaskInfo(taskPath)
		if err != nil {
			slog.Error("Failed to load task info", "task", taskID, "error", err)
			continue
		}

		history, err := s.loadTaskHistory(taskPath)
		if err != nil {
			slog.Error("Failed to load task history", "task", taskID, "error", err)
		}

		task.History = history
		tasks = append(tasks, task)
	}

	return tasks, nil
}

func (s *Server) loadTaskInfo(taskPath string) (TaskInfo, error) {
	var task TaskInfo
	taskFilePath := filepath.Join(taskPath, "task.json")

	data, err := os.ReadFile(taskFilePath)
	if err != nil {
		return task, err
	}

	if err := json.Unmarshal(data, &task); err != nil {
		return task, err
	}

	return task, nil
}

func (s *Server) loadTaskHistory(taskPath string) ([]Message, error) {
	historyFilePath := filepath.Join(taskPath, "history.jsonl")

	file, err := os.Open(historyFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return []Message{}, nil
		}
		return nil, err
	}
	defer file.Close()

	var messages []Message
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var msg Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			slog.Error("Failed to parse history message", "error", err)
			continue
		}
		messages = append(messages, msg)
	}

	return messages, scanner.Err()
}

func (s *Server) handleCron(w http.ResponseWriter, r *http.Request) {
	agentKey := r.URL.Query().Get("agent")
	if agentKey == "" {
		http.Error(w, "Agent parameter is required", http.StatusBadRequest)
		return
	}

	s.cfgMu.RLock()
	agent, exists := s.cfg.Agents[agentKey]
	s.cfgMu.RUnlock()

	if !exists {
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	workspace := agent.Workspace
	cronDir := filepath.Join(workspace, "cron")

	cronJobs, err := s.loadCronJobs(cronDir)
	if err != nil {
		slog.Error("Failed to load cron jobs", "error", err)
		http.Error(w, "Failed to load cron jobs", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"cronJobs": cronJobs,
	})
}

type CronJob struct {
	ID          string  `json:"id"`
	Schedule    string  `json:"schedule"`
	Description string  `json:"description"`
	Status      string  `json:"status"`
	LastRun     *string `json:"last_run,omitempty"`
	NextRun     *string `json:"next_run,omitempty"`
	CreatedAt   string  `json:"created_at"`
	UpdatedAt   string  `json:"updated_at"`
	Channel     string  `json:"channel"`
	ChatID      string  `json:"chat_id"`
}

func (s *Server) loadCronJobs(cronDir string) ([]CronJob, error) {
	var cronJobs []CronJob

	cronFiles, err := os.ReadDir(cronDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []CronJob{}, nil
		}
		return nil, err
	}

	for _, cronFile := range cronFiles {
		if cronFile.IsDir() || !strings.HasSuffix(cronFile.Name(), ".json") {
			continue
		}

		cronID := strings.TrimSuffix(cronFile.Name(), ".json")
		cronFilePath := filepath.Join(cronDir, cronFile.Name())

		cronJob, err := s.loadCronJob(cronFilePath)
		if err != nil {
			slog.Error("Failed to load cron job", "cron_id", cronID, "error", err)
			continue
		}

		cronJobs = append(cronJobs, cronJob)
	}

	return cronJobs, nil
}

func (s *Server) loadCronJob(filePath string) (CronJob, error) {
	var cronJob CronJob

	data, err := os.ReadFile(filePath)
	if err != nil {
		return cronJob, err
	}

	if err := json.Unmarshal(data, &cronJob); err != nil {
		return cronJob, err
	}

	return cronJob, nil
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.cfgMu.RLock()
		defer s.cfgMu.RUnlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"config": s.cfg,
		})
	case "PUT":
		var newConfig config.Config
		if err := json.NewDecoder(r.Body).Decode(&newConfig); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		s.cfgMu.Lock()
		s.cfg = &newConfig
		s.cfgMu.Unlock()

		if err := config.SaveConfig(s.wangshuPath, &newConfig); err != nil {
			slog.Error("Failed to save config", "error", err)
			http.Error(w, "Failed to save config", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
		})
	default:
	}
}

func (s *Server) handleInstance(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		s.getInstanceStatus(w, r)
	case "POST":
		action := r.URL.Query().Get("action")
		switch action {
		case "start":
			s.startInstance(w, r)
		case "stop":
			s.stopInstance(w, r)
		case "restart":
			s.restartInstance(w, r)
		default:
			http.Error(w, "Invalid action", http.StatusBadRequest)
		}
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) getInstanceStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.processManager.GetStatus()
	if err != nil {
		slog.Error("Failed to get instance status", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status": status,
	})
}

func (s *Server) startInstance(w http.ResponseWriter, r *http.Request) {
	if err := s.processManager.Start(false); err != nil {
		slog.Error("Failed to start instance", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Instance started successfully",
	})
}

func (s *Server) stopInstance(w http.ResponseWriter, r *http.Request) {
	if err := s.processManager.Stop(); err != nil {
		slog.Error("Failed to stop instance", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Instance stopped successfully",
	})
}

func (s *Server) restartInstance(w http.ResponseWriter, r *http.Request) {
	if err := s.processManager.Restart(); err != nil {
		slog.Error("Failed to restart instance", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Instance restarted successfully",
	})
}

func main() {
	wangshuPath := "~/.wangshu/config.json"
	if len(os.Args) > 1 {
		wangshuPath = os.Args[1]
	}

	cfg, err := config.LoadConfig(wangshuPath)
	if err != nil {
		slog.Error("Failed to load config", "error", err)
		os.Exit(1)
	}

	server, err := NewServer(cfg, wangshuPath)
	if err != nil {
		slog.Error("Failed to create server", "error", err)
		os.Exit(1)
	}

	if err := server.processManager.AutoStartIfNotRunning(); err != nil {
		slog.Warn("Failed to auto-start wangshu instance", "error", err)
	}

	if err := server.Start(); err != nil {
		slog.Error("Server error", "error", err)
		os.Exit(1)
	}
}
