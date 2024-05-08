package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"next-terminal/server/common/nt"
	"path"
	"strconv"
	"strings"
	"time"

	"next-terminal/server/common/guacamole"
	"next-terminal/server/common/term"
	"next-terminal/server/config"
	"next-terminal/server/dto"
	"next-terminal/server/global/session"
	"next-terminal/server/model"
	"next-terminal/server/repository"
	"next-terminal/server/service"
	"next-terminal/server/utils"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

const (
	Closed    = 0
	Connected = 1
	Data      = 2
	Resize    = 3
	Ping      = 4
)

type WebTerminalApi struct {
}

func (api WebTerminalApi) SshEndpoint(c echo.Context) error {
	ws, err := UpGrader.Upgrade(c.Response().Writer, c.Request(), nil)
	if err != nil {
		return err
	}

	defer func() {
		_ = ws.Close()
	}()
	ctx := context.TODO()

	sessionId := c.Param("id")
	cols, _ := strconv.Atoi(c.QueryParam("cols"))
	rows, _ := strconv.Atoi(c.QueryParam("rows"))

	s, err := service.SessionService.FindByIdAndDecrypt(ctx, sessionId)
	if err != nil {
		return WriteMessage(ws, dto.NewMessage(Closed, "获取会话或解密数据失败"))
	}

	if err := api.permissionCheck(c, s.AssetId); err != nil {
		return WriteMessage(ws, dto.NewMessage(Closed, err.Error()))
	}

	var (
		username   = s.Username
		password   = s.Password
		privateKey = s.PrivateKey
		passphrase = s.Passphrase
		ip         = s.IP
		port       = s.Port
	)

	if s.AccessGatewayId != "" && s.AccessGatewayId != "-" {
		g, err := service.GatewayService.GetGatewayById(s.AccessGatewayId)
		if err != nil {
			return WriteMessage(ws, dto.NewMessage(Closed, "获取接入网关失败："+err.Error()))
		}

		defer g.CloseSshTunnel(s.ID)
		exposedIP, exposedPort, err := g.OpenSshTunnel(s.ID, ip, port)
		if err != nil {
			return WriteMessage(ws, dto.NewMessage(Closed, "创建隧道失败："+err.Error()))
		}
		ip = exposedIP
		port = exposedPort
	}

	recording := ""
	var isRecording = false
	property, err := repository.PropertyRepository.FindByName(ctx, guacamole.EnableRecording)
	if err == nil && property.Value == "true" {
		isRecording = true
	}

	if isRecording {
		recording = path.Join(config.GlobalCfg.Guacd.Recording, sessionId, "recording.cast")
	}

	attributes, err := repository.AssetRepository.FindAssetAttrMapByAssetId(ctx, s.AssetId)
	if err != nil {
		return WriteMessage(ws, dto.NewMessage(Closed, "获取资产属性失败："+err.Error()))
	}

	var xterm = "xterm-256color"
	var nextTerminal *term.NextTerminal
	if "true" == attributes[nt.SocksProxyEnable] {
		nextTerminal, err = term.NewNextTerminalUseSocks(ip, port, username, password, privateKey, passphrase, rows, cols, recording, xterm, true, attributes[nt.SocksProxyHost], attributes[nt.SocksProxyPort], attributes[nt.SocksProxyUsername], attributes[nt.SocksProxyPassword])
	} else {
		nextTerminal, err = term.NewNextTerminal(ip, port, username, password, privateKey, passphrase, rows, cols, recording, xterm, true)
	}

	if err != nil {
		return WriteMessage(ws, dto.NewMessage(Closed, "创建SSH客户端失败："+err.Error()))
	}

	if err := nextTerminal.RequestPty(xterm, rows, cols); err != nil {
		return err
	}

	if err := nextTerminal.Shell(); err != nil {
		return err
	}

	sessionForUpdate := model.Session{
		ConnectionId: sessionId,
		Width:        cols,
		Height:       rows,
		Status:       nt.Connecting,
		Recording:    recording,
	}
	if sessionForUpdate.Recording == "" {
		// 未录屏时无需审计
		sessionForUpdate.Reviewed = true
	}
	// 创建新会话
	if err := repository.SessionRepository.UpdateById(ctx, &sessionForUpdate, sessionId); err != nil {
		return err
	}

	if err := WriteMessage(ws, dto.NewMessage(Connected, "")); err != nil {
		return err
	}

	nextSession := &session.Session{
		ID:           s.ID,
		Protocol:     s.Protocol,
		Mode:         s.Mode,
		WebSocket:    ws,
		GuacdTunnel:  nil,
		NextTerminal: nextTerminal,
		Observer:     session.NewObserver(s.ID),
	}
	session.GlobalSessionManager.Add(nextSession)

	termHandler := NewTermHandler(s.Creator, s.AssetId, sessionId, isRecording, ws, nextTerminal)
	termHandler.Start()
	defer termHandler.Stop()
	// 设置定时任务 超时 主动退出
	isClose := false
	timer := time.NewTimer(30 * time.Minute)
	isDoChan := make(chan bool, 1)
	go func() {
		for {
			select {
			case <-timer.C: // 超时主动退出
				if isClose { // 已经退出
					return
				}
				service.SessionService.CloseSessionById(sessionId, ForcedTimeoutDisconnect, "30分钟未操作，主动断开连接。如需继续操作，请重新连接！")
			case <-isDoChan: // 用户正常使用
				timer.Reset(30 * time.Minute)
			}
		}
	}()
	// 清空历史命令
	_ = termHandler.Write([]byte("history -c\n"))
	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			// web socket会话关闭后主动关闭ssh会话
			service.SessionService.CloseSessionById(sessionId, Normal, "用户正常退出")
			isClose = true
			break
		}

		msg, err := dto.ParseMessage(string(message))
		if err != nil {
			continue
		}

		if msg.Type != Ping {
			isDoChan <- true
		}
		switch msg.Type {
		case Resize:
			decodeString, err := base64.StdEncoding.DecodeString(msg.Content)
			if err != nil {
				continue
			}
			var winSize dto.WindowSize
			err = json.Unmarshal(decodeString, &winSize)
			if err != nil {
				continue
			}
			if err := termHandler.WindowChange(winSize.Rows, winSize.Cols); err != nil {
			}
			_ = repository.SessionRepository.UpdateWindowSizeById(ctx, winSize.Rows, winSize.Cols, sessionId)
		case Data:
			offset := termHandler.offset
			fmt.Println("== write ", []byte(msg.Content))
			switch msg.Content {
			case string([]byte{27, 91, 65}): // 上 记录回显示命令
				termHandler.review = true
			case string([]byte{27, 91, 66}): // 下 记录回显示命令
				termHandler.review = true
			case string([]byte{27, 91, 67}): // 右 记录光标位置记录命令
				if offset < len(termHandler.command) {
					offset++
				}
			case string([]byte{27, 91, 68}): // 左 记录光标位置记录命令
				if offset > 0 {
					offset--
				}
			case string([]byte{13}): // 回车 执行命令前 检测命令 是否危险
				// check command danger
				if checkCommandDanger(termHandler.command) { // 下发 ctrl + c
					err := termHandler.WriteCancel()
					if err != nil {
						service.SessionService.CloseSessionById(sessionId, TunnelClosed, "远程连接已关闭")
					}

					termHandler.command = ""
					termHandler.offset = 0
					// warning
					service.SessionService.WarningSessionById(sessionId, DangerCommandWarning, "\r\nCommand `rm` is forbidden")
					continue
				}
				termHandler.command = ""
				offset = 0
			case string([]byte{27, 91, 51, 126}): // DEL 执行命令前 检测命令 是否危险
				if len(termHandler.command) > 0 {
					if offset != len(termHandler.command) {
						termHandler.command = termHandler.command[:offset] + termHandler.command[offset+1:]
					}
				}
			case string([]byte{127}): // 删除 执行命令前 检测命令 是否危险
				if len(termHandler.command) > 0 && offset > 0 {

					if offset != len(termHandler.command) {
						offset--
						termHandler.command = termHandler.command[:offset] + termHandler.command[offset+1:]
					} else {
						offset--
						termHandler.command = termHandler.command[:offset]
					}
				}
			case string([]byte{3}): // ctrl C 取消记录的命令
				termHandler.command = ""
				termHandler.offset = 0
				termHandler.review = false
			default:
				offset += len(msg.Content)
				if len(termHandler.command) != termHandler.offset { // 中间写入
					termHandler.command = termHandler.command[:termHandler.offset] + msg.Content + termHandler.command[termHandler.offset:]
				} else {
					termHandler.command += msg.Content
				}
				// 判断是否有 回车
				cmds := strings.Split(termHandler.command, string([]byte{13}))
				isDanger := false
				for i := 0; len(cmds) > 1 && i < len(cmds); i++ {
					if checkCommandDanger(cmds[i]) {
						isDanger = true
						break
					}
				}
				if isDanger {
					err := termHandler.WriteCancel()
					if err != nil {
						service.SessionService.CloseSessionById(sessionId, TunnelClosed, "远程连接已关闭")
					}
					termHandler.command = ""
					termHandler.offset = 0
					// warning
					service.SessionService.WarningSessionById(sessionId, DangerCommandWarning, "\r\nCommand `rm` is forbidden")
					continue
				}
			}
			if !termHandler.review {
				termHandler.offset = offset
			}
			// 危险命令 直接下发 ctrl+c 终止命令执行
			fmt.Println(termHandler.command, termHandler.offset)

			input := []byte(msg.Content)
			err := termHandler.Write(input)
			if err != nil {
				service.SessionService.CloseSessionById(sessionId, TunnelClosed, "远程连接已关闭")
			}
		case Ping:
			err := termHandler.SendRequest()
			if err != nil {
				service.SessionService.CloseSessionById(sessionId, TunnelClosed, "远程连接已关闭")
			} else {
				_ = termHandler.SendMessageToWebSocket(dto.NewMessage(Ping, ""))
			}

		}
	}
	return err
}
func checkCommandDanger(command string) bool {
	fmt.Println("==== check cmd ", command)
	cmds := strings.Split(command, "&&")
	for _, cmd := range cmds {
		cmd = strings.TrimSpace(cmd)
		if len(cmd) > 3 && cmd[:3] == "rm " {
			return true
		}
	}
	return false
}
func (api WebTerminalApi) SshMonitorEndpoint(c echo.Context) error {
	ws, err := UpGrader.Upgrade(c.Response().Writer, c.Request(), nil)
	if err != nil {
		return err
	}

	defer func() {
		_ = ws.Close()
	}()
	ctx := context.TODO()

	sessionId := c.Param("id")
	s, err := repository.SessionRepository.FindById(ctx, sessionId)
	if err != nil {
		return WriteMessage(ws, dto.NewMessage(Closed, "获取会话失败"))
	}

	nextSession := session.GlobalSessionManager.GetById(sessionId)
	if nextSession == nil {
		return WriteMessage(ws, dto.NewMessage(Closed, "会话已离线"))
	}

	obId := utils.UUID()
	obSession := &session.Session{
		ID:        obId,
		Protocol:  s.Protocol,
		Mode:      s.Mode,
		WebSocket: ws,
	}
	nextSession.Observer.Add(obSession)

	for {
		_, _, err := ws.ReadMessage()
		if err != nil {
			nextSession.Observer.Del(obId)
			break
		}
	}
	return nil
}

func (api WebTerminalApi) permissionCheck(c echo.Context, assetId string) error {
	user, _ := GetCurrentAccount(c)
	if nt.TypeUser == user.Type {
		// 检测是否有访问权限 TODO
		//assetIds, err := repository.ResourceSharerRepository.FindAssetIdsByUserId(context.TODO(), user.ID)
		//if err != nil {
		//	return err
		//}
		//
		//if !utils.Contains(assetIds, assetId) {
		//	return errors.New("您没有权限访问此资产")
		//}
	}
	return nil
}

func WriteMessage(ws *websocket.Conn, msg dto.Message) error {
	message := []byte(msg.ToString())
	return ws.WriteMessage(websocket.TextMessage, message)
}

func CreateNextTerminalBySession(session model.Session) (*term.NextTerminal, error) {
	var (
		username   = session.Username
		password   = session.Password
		privateKey = session.PrivateKey
		passphrase = session.Passphrase
		ip         = session.IP
		port       = session.Port
	)
	return term.NewNextTerminal(ip, port, username, password, privateKey, passphrase, 10, 10, "", "", false)
}
