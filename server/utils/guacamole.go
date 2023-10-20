package utils

import (
	"encoding/base64"
	"strconv"

	"next-terminal/server/guacd"

	"github.com/gorilla/websocket"
)

func Disconnect(ws *WebSocketConn, code int, reason string) {

	// guacd 无法处理中文字符，所以进行了base64编码。
	encodeReason := base64.StdEncoding.EncodeToString([]byte(reason))
	err := guacd.NewInstruction("error", encodeReason, strconv.Itoa(code))
	ws.Locker.Lock()
	defer ws.Locker.Unlock()
	_ = ws.Ws.WriteMessage(websocket.TextMessage, []byte(err.String()))
	disconnect := guacd.NewInstruction("disconnect")
	_ = ws.Ws.WriteMessage(websocket.TextMessage, []byte(disconnect.String()))
}
