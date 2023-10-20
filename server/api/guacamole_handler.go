package api

import (
	"context"

	"next-terminal/server/guacd"
	"next-terminal/server/log"
	"next-terminal/server/utils"

	"github.com/gorilla/websocket"
)

type GuacamoleHandler struct {
	ws     *utils.WebSocketConn
	tunnel *guacd.Tunnel
	ctx    context.Context
	cancel context.CancelFunc
}

func NewGuacamoleHandler(ws *utils.WebSocketConn, tunnel *guacd.Tunnel) *GuacamoleHandler {
	ctx, cancel := context.WithCancel(context.Background())
	return &GuacamoleHandler{
		ws:     ws,
		tunnel: tunnel,
		ctx:    ctx,
		cancel: cancel,
	}
}

func (r GuacamoleHandler) Start() {
	go func() {
		for {
			select {
			case <-r.ctx.Done():
				return
			default:
				instruction, err := r.tunnel.Read()
				if err != nil {
					utils.Disconnect(r.ws, TunnelClosed, "远程连接已关闭")
					return
				}
				if len(instruction) == 0 {
					continue
				}
				r.ws.Locker.Lock()
				err = r.ws.Ws.WriteMessage(websocket.TextMessage, instruction)
				r.ws.Locker.Unlock()
				if err != nil {
					log.Debugf("WebSocket写入失败，即将关闭Guacd连接...")
					return
				}
			}
		}
	}()
}

func (r GuacamoleHandler) Stop() {
	r.cancel()
}
