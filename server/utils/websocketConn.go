/*
   @Author: 1usir
   @Description:
   @File: websocketConn
   @Version: 1.0.0
   @Date: 2023/10/20 14:54
*/

package utils

import (
	"github.com/gorilla/websocket"
	"sync"
)

type WebSocketConn struct {
	Ws     *websocket.Conn
	Locker sync.Mutex
}

func NewWebSocketConn(ws *websocket.Conn) *WebSocketConn {
	return &WebSocketConn{
		Ws:     ws,
		Locker: sync.Mutex{},
	}
}
