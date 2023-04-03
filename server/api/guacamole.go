package api

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"next-terminal/server/config"
	"next-terminal/server/constant"
	"next-terminal/server/global/session"
	"next-terminal/server/guacd"
	"next-terminal/server/log"
	"next-terminal/server/model"
	"next-terminal/server/repository"
	"next-terminal/server/service"
	"next-terminal/server/utils"
	"path"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
	"github.com/labstack/echo/v4"
)

const (
	TunnelClosed             int = -1
	Normal                   int = 0
	NotFoundSession          int = 800
	NewTunnelError           int = 801
	ForcedDisconnect         int = 802
	AccessGatewayUnAvailable int = 803
	AccessGatewayCreateError int = 804
	AssetNotActive           int = 805
	NewSshClientError        int = 806
	ForcedTimeoutDisconnect  int = 807
)

var UpGrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
	Subprotocols: []string{"guacamole"},
}

type GuacamoleApi struct {
}

func (api GuacamoleApi) Guacamole(c echo.Context) error {
	ws, err := UpGrader.Upgrade(c.Response().Writer, c.Request(), nil)
	if err != nil {
		log.Errorf("升级为WebSocket协议失败：%v", err.Error())
		return err
	}
	ctx := context.TODO()
	width := c.QueryParam("width")
	height := c.QueryParam("height")
	dpi := c.QueryParam("dpi")
	sessionId := c.Param("id")

	intWidth, _ := strconv.Atoi(width)
	intHeight, _ := strconv.Atoi(height)

	configuration := guacd.NewConfiguration()

	propertyMap := repository.PropertyRepository.FindAllMap(ctx)

	configuration.SetParameter("width", width)
	configuration.SetParameter("height", height)
	configuration.SetParameter("dpi", dpi)
	s, err := service.SessionService.FindByIdAndDecrypt(ctx, sessionId)
	if err != nil {
		return err
	}
	api.setConfig(propertyMap, s, configuration)

	if s.AccessGatewayId != "" && s.AccessGatewayId != "-" {
		g, err := service.GatewayService.GetGatewayAndReconnectById(s.AccessGatewayId)
		if err != nil {
			utils.Disconnect(ws, AccessGatewayUnAvailable, "获取接入网关失败："+err.Error())
			return nil
		}
		if !g.Connected {
			utils.Disconnect(ws, AccessGatewayUnAvailable, "接入网关不可用："+g.Message)
			return nil
		}
		exposedIP, exposedPort, err := g.OpenSshTunnel(s.ID, s.IP, s.Port)
		if err != nil {
			utils.Disconnect(ws, AccessGatewayCreateError, "创建SSH隧道失败："+err.Error())
			return nil
		}
		s.IP = exposedIP
		s.Port = exposedPort
		defer g.CloseSshTunnel(s.ID)
	}

	configuration.SetParameter("hostname", s.IP)
	configuration.SetParameter("port", strconv.Itoa(s.Port))

	// 加载资产配置的属性，优先级比全局配置的高，因此最后加载，覆盖掉全局配置
	attributes, err := repository.AssetRepository.FindAssetAttrMapByAssetId(ctx, s.AssetId)
	if err != nil {
		return err
	}
	if len(attributes) > 0 {
		api.setAssetConfig(attributes, s, configuration)
	}
	for name := range configuration.Parameters {
		// 替换数据库空格字符串占位符为真正的空格
		if configuration.Parameters[name] == "-" {
			configuration.Parameters[name] = ""
		}
	}

	addr := config.GlobalCfg.Guacd.Hostname + ":" + strconv.Itoa(config.GlobalCfg.Guacd.Port)
	asset := fmt.Sprintf("%s:%s", configuration.GetParameter("hostname"), configuration.GetParameter("port"))
	log.Debugf("[%v] 新建 guacd 会话, guacd=%v, asset=%v", sessionId, addr, asset)

	guacdTunnel, err := guacd.NewTunnel(addr, configuration)
	if err != nil {
		utils.Disconnect(ws, NewTunnelError, err.Error())
		log.Printf("[%v] 建立连接失败: %v", sessionId, err.Error())
		return err
	}

	nextSession := &session.Session{
		ID:          sessionId,
		Protocol:    s.Protocol,
		Mode:        s.Mode,
		WebSocket:   ws,
		GuacdTunnel: guacdTunnel,
	}

	if configuration.Protocol == constant.SSH {
		nextTerminal, err := CreateNextTerminalBySession(s)
		if err != nil {
			utils.Disconnect(ws, NewSshClientError, "建立SSH客户端失败: "+err.Error())
			log.Printf("[%v] 建立 ssh 客户端失败: %v", sessionId, err.Error())
			return err
		}
		nextSession.NextTerminal = nextTerminal
	}

	nextSession.Observer = session.NewObserver(sessionId)
	session.GlobalSessionManager.Add <- nextSession
	go nextSession.Observer.Start()
	sess := model.Session{
		ConnectionId: guacdTunnel.UUID,
		Width:        intWidth,
		Height:       intHeight,
		Status:       constant.Connecting,
		Recording:    configuration.GetParameter(guacd.RecordingPath),
	}
	if sess.Recording == "" {
		// 未录屏时无需审计
		sess.Reviewed = true
	}
	// 创建新会话
	log.Debugf("[%v] 新建会话成功: %v", sessionId, sess.ConnectionId)
	if err := repository.SessionRepository.UpdateById(ctx, &sess, sessionId); err != nil {
		return err
	}

	guacamoleHandler := NewGuacamoleHandler(ws, guacdTunnel)
	guacamoleHandler.Start()
	defer guacamoleHandler.Stop()

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
	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			isClose = true
			log.Debugf("[%v] WebSocket已关闭, %v", sessionId, err.Error())
			// guacdTunnel.Read() 会阻塞，所以要先把guacdTunnel客户端关闭，才能退出Guacd循环
			_ = guacdTunnel.Close()

			service.SessionService.CloseSessionById(sessionId, Normal, "用户正常退出")
			return nil
		}
		// 5.mouse 鼠标在动
		// 3.key 键盘输入
		if (len(message) > 7 && string(message[:7]) == "5.mouse") || (len(message) > 5 && string(message[:5]) == "3.key") {
			isDoChan <- true
		}
		_, err = guacdTunnel.WriteAndFlush(message)
		if err != nil {
			isClose = true
			service.SessionService.CloseSessionById(sessionId, TunnelClosed, "远程连接已关闭")
			return nil
		}
	}
}

func (api GuacamoleApi) setAssetConfig(attributes map[string]string, s model.Session, configuration *guacd.Configuration) {
	for key, value := range attributes {
		if guacd.DrivePath == key {
			// 忽略该参数
			continue
		}
		if guacd.EnableDrive == key && value == "true" {
			storageId := attributes[guacd.DrivePath]
			if storageId == "" || storageId == "-" {
				// 默认空间ID和用户ID相同
				storageId = s.Creator
			}
			realPath := path.Join(service.StorageService.GetBaseDrivePath(), storageId)
			configuration.SetParameter(guacd.EnableDrive, "true")
			configuration.SetParameter(guacd.DriveName, "Filesystem")
			configuration.SetParameter(guacd.DrivePath, realPath)
			log.Debugf("[%v] 会话 %v:%v 映射目录地址为 %v", s.ID, s.IP, s.Port, realPath)
		} else {
			configuration.SetParameter(key, value)
		}
	}
}

// CheckMonitor 监控检测，当检测到用户30分钟内都没有操作时，主动端口连接
func (api GuacamoleApi) CheckMonitor(sessionId string) error {

	//todo ： 通过ws 调用监控接口，读取message 长度， 19 27 为正常长度。 超过该长度 存在用户输入。 超过30分钟都没有超过这个长度则认为超时 主动断开连接
	// nginx 设置免登录地址
	u := url.URL{Scheme: "ws", Host: "127.0.0.1:8081", Path: ""}
	u.Path = fmt.Sprintf("sessions/%s/tunnel-monitor", sessionId)

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return err
	}
	defer c.Close()

	for {
		_, message, err := c.ReadMessage()
		if err != nil {
			log.Println("read:", err)
			return err
		}
		fmt.Printf("recv: %s\n", message)
	}

}
func (api GuacamoleApi) GuacamoleMonitor(c echo.Context) error {
	ws, err := UpGrader.Upgrade(c.Response().Writer, c.Request(), nil)
	if err != nil {
		log.Errorf("升级为WebSocket协议失败：%v", err.Error())
		return err
	}
	ctx := context.TODO()
	sessionId := c.Param("id")

	s, err := repository.SessionRepository.FindById(ctx, sessionId)
	if err != nil {
		return err
	}
	if s.Status != constant.Connected {
		utils.Disconnect(ws, AssetNotActive, "会话离线")
		return nil
	}
	connectionId := s.ConnectionId
	configuration := guacd.NewConfiguration()
	configuration.ConnectionID = connectionId
	sessionId = s.ID
	configuration.SetParameter("width", strconv.Itoa(s.Width))
	configuration.SetParameter("height", strconv.Itoa(s.Height))
	configuration.SetParameter("dpi", "96")

	addr := config.GlobalCfg.Guacd.Hostname + ":" + strconv.Itoa(config.GlobalCfg.Guacd.Port)
	asset := fmt.Sprintf("%s:%s", configuration.GetParameter("hostname"), configuration.GetParameter("port"))
	log.Debugf("[%v] 新建 guacd 会话, guacd=%v, asset=%v", sessionId, addr, asset)

	guacdTunnel, err := guacd.NewTunnel(addr, configuration)
	if err != nil {
		utils.Disconnect(ws, NewTunnelError, err.Error())
		log.Printf("[%v] 建立连接失败: %v", sessionId, err.Error())
		return err
	}

	nextSession := &session.Session{
		ID:          sessionId,
		Protocol:    s.Protocol,
		Mode:        s.Mode,
		WebSocket:   ws,
		GuacdTunnel: guacdTunnel,
	}

	// 要监控会话
	forObsSession := session.GlobalSessionManager.GetById(sessionId)
	if forObsSession == nil {
		utils.Disconnect(ws, NotFoundSession, "获取会话失败")
		return nil
	}
	nextSession.ID = utils.UUID()
	forObsSession.Observer.Add <- nextSession
	log.Debugf("[%v:%v] 观察者[%v]加入会话[%v]", sessionId, connectionId, nextSession.ID, s.ConnectionId)

	guacamoleHandler := NewGuacamoleHandler(ws, guacdTunnel)
	guacamoleHandler.Start()
	defer guacamoleHandler.Stop()

	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			log.Debugf("[%v:%v] WebSocket已关闭, %v", sessionId, connectionId, err.Error())
			// guacdTunnel.Read() 会阻塞，所以要先把guacdTunnel客户端关闭，才能退出Guacd循环
			_ = guacdTunnel.Close()

			observerId := nextSession.ID
			forObsSession.Observer.Del <- observerId
			log.Debugf("[%v:%v] 观察者[%v]退出会话", sessionId, connectionId, observerId)
			return nil
		}
		_, err = guacdTunnel.WriteAndFlush(message)
		if err != nil {
			service.SessionService.CloseSessionById(sessionId, TunnelClosed, "远程连接已关闭")
			return nil
		}
	}
}

func (api GuacamoleApi) setConfig(propertyMap map[string]string, s model.Session, configuration *guacd.Configuration) {
	if propertyMap[guacd.EnableRecording] == "true" {
		configuration.SetParameter(guacd.RecordingPath, path.Join(config.GlobalCfg.Guacd.Recording, s.ID))
		configuration.SetParameter(guacd.CreateRecordingPath, "true")
	} else {
		configuration.SetParameter(guacd.RecordingPath, "")
	}

	configuration.Protocol = s.Protocol
	switch configuration.Protocol {
	case "rdp":
		configuration.SetParameter("username", s.Username)
		configuration.SetParameter("password", s.Password)

		configuration.SetParameter("security", "any")
		configuration.SetParameter("ignore-cert", "true")
		configuration.SetParameter("create-drive-path", "true")
		configuration.SetParameter("resize-method", "reconnect")
		configuration.SetParameter(guacd.EnableWallpaper, propertyMap[guacd.EnableWallpaper])
		configuration.SetParameter(guacd.EnableTheming, propertyMap[guacd.EnableTheming])
		configuration.SetParameter(guacd.EnableFontSmoothing, propertyMap[guacd.EnableFontSmoothing])
		configuration.SetParameter(guacd.EnableFullWindowDrag, propertyMap[guacd.EnableFullWindowDrag])
		configuration.SetParameter(guacd.EnableDesktopComposition, propertyMap[guacd.EnableDesktopComposition])
		configuration.SetParameter(guacd.EnableMenuAnimations, propertyMap[guacd.EnableMenuAnimations])
		configuration.SetParameter(guacd.DisableBitmapCaching, propertyMap[guacd.DisableBitmapCaching])
		configuration.SetParameter(guacd.DisableOffscreenCaching, propertyMap[guacd.DisableOffscreenCaching])
		configuration.SetParameter(guacd.ColorDepth, propertyMap[guacd.ColorDepth])
		configuration.SetParameter(guacd.ForceLossless, propertyMap[guacd.ForceLossless])
		configuration.SetParameter(guacd.PreConnectionId, propertyMap[guacd.PreConnectionId])
		configuration.SetParameter(guacd.PreConnectionBlob, propertyMap[guacd.PreConnectionBlob])
	case "ssh":
		if len(s.PrivateKey) > 0 && s.PrivateKey != "-" {
			configuration.SetParameter("username", s.Username)
			configuration.SetParameter("private-key", s.PrivateKey)
			configuration.SetParameter("passphrase", s.Passphrase)
		} else {
			configuration.SetParameter("username", s.Username)
			configuration.SetParameter("password", s.Password)
		}

		configuration.SetParameter(guacd.FontSize, propertyMap[guacd.FontSize])
		configuration.SetParameter(guacd.FontName, propertyMap[guacd.FontName])
		configuration.SetParameter(guacd.ColorScheme, propertyMap[guacd.ColorScheme])
		configuration.SetParameter(guacd.Backspace, propertyMap[guacd.Backspace])
		configuration.SetParameter(guacd.TerminalType, propertyMap[guacd.TerminalType])
	case "vnc":
		configuration.SetParameter("username", s.Username)
		configuration.SetParameter("password", s.Password)
	case "telnet":
		configuration.SetParameter("username", s.Username)
		configuration.SetParameter("password", s.Password)

		configuration.SetParameter(guacd.FontSize, propertyMap[guacd.FontSize])
		configuration.SetParameter(guacd.FontName, propertyMap[guacd.FontName])
		configuration.SetParameter(guacd.ColorScheme, propertyMap[guacd.ColorScheme])
		configuration.SetParameter(guacd.Backspace, propertyMap[guacd.Backspace])
		configuration.SetParameter(guacd.TerminalType, propertyMap[guacd.TerminalType])
	case "kubernetes":
		configuration.SetParameter(guacd.FontSize, propertyMap[guacd.FontSize])
		configuration.SetParameter(guacd.FontName, propertyMap[guacd.FontName])
		configuration.SetParameter(guacd.ColorScheme, propertyMap[guacd.ColorScheme])
		configuration.SetParameter(guacd.Backspace, propertyMap[guacd.Backspace])
		configuration.SetParameter(guacd.TerminalType, propertyMap[guacd.TerminalType])
	default:

	}
}
