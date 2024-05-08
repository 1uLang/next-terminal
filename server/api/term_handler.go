package api

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
	"next-terminal/server/common/term"
	"next-terminal/server/dto"
	"next-terminal/server/global/session"
)

type TermHandler struct {
	sessionId    string
	isRecording  bool
	webSocket    *websocket.Conn
	nextTerminal *term.NextTerminal
	ctx          context.Context
	cancel       context.CancelFunc
	dataChan     chan rune
	tick         *time.Ticker
	mutex        sync.Mutex
	buf          bytes.Buffer

	done1, done2 bool
	command      string //命令
	review       bool   //翻看历史记录
	tab          bool   //tab补全代码
	offset       int    //索引
	isDanger     bool   //清空命令
}

func NewTermHandler(userId, assetId, sessionId string, isRecording bool, ws *websocket.Conn, nextTerminal *term.NextTerminal) *TermHandler {
	ctx, cancel := context.WithCancel(context.Background())
	tick := time.NewTicker(time.Millisecond * time.Duration(60))

	return &TermHandler{
		sessionId:    sessionId,
		isRecording:  isRecording,
		webSocket:    ws,
		nextTerminal: nextTerminal,
		ctx:          ctx,
		cancel:       cancel,
		dataChan:     make(chan rune),
		tick:         tick,
	}
}

func (r *TermHandler) Start() {
	go r.readFormTunnel()
	go r.writeToWebsocket()
}

func (r *TermHandler) Stop() {
	// 会话结束时记录最后一个命令
	r.tick.Stop()
	r.cancel()
}

func (r *TermHandler) readFormTunnel() {
	for {
		select {
		case <-r.ctx.Done():
			return
		default:
			rn, size, err := r.nextTerminal.StdoutReader.ReadRune()
			if err != nil {
				return
			}
			if size > 0 {
				r.dataChan <- rn
			}
		}
	}
}

func (r *TermHandler) writeToWebsocket() {
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.tick.C:
			s := r.buf.String()
			if s == "" {
				continue
			}
			if !r.done1 || !r.done2 {
				if i := strings.Index(s, "history -c"); i != -1 {
					s = s[:i]
					if r.done1 {
						r.done2 = true
					}
					r.done1 = true
					fmt.Println("?")
				}
			}
			fmt.Println("??????", []byte(s), r.review && s != "", r.isDanger)
			if r.review {
				r.review = false
				if s == string([]byte{7}) { // 不变

				} else if idx := strings.Index(s, string([]byte{27, 91, 75})); idx != -1 && idx != len(s)-3 { // 清空部分命令
					r.command = r.command[:len(r.command)-idx]
				} else if idx := strings.Index(s, string([]byte{27, 91})); idx != -1 && !strings.Contains(s, string([]byte{27, 91, 75})) { // 清空命令
					if idx < len(r.command) {
						r.command = r.command[idx+1:] + s[idx+4:]
					} else {
						r.command = s[idx+4:]
					}
				} else if idx := strings.LastIndex(s, string([]byte{8})); idx != -1 { // 替换部分
					tmp := s
					if idx := strings.Index(s, string([]byte{27, 91, 75})); idx != -1 {
						tmp = s[:idx]
					}
					if idx < len(r.command) {
						r.command = r.command[:len(r.command)-idx-1] + tmp[idx+1:]
					} else {
						r.command = tmp[idx+1:]
					}
				} else { // 命令追加
					r.command += s
				}
				r.offset = len(r.command)
				fmt.Println("review command ", r.command)
				fmt.Println("review command ", []byte(r.command))
			}
			if r.tab {
				r.tab = false
				if s == string([]byte{7}) { // 不变
				} else { // 命令追加
					r.command += s
				}
				r.offset = len(r.command)
				fmt.Println("tab command ", r.command)
				fmt.Println("tab command ", []byte(r.command))
			}

			if r.isDanger && strings.HasPrefix(s, string([]byte{94, 67})) {
				s = strings.TrimPrefix(s, string([]byte{94, 67}))
				r.isDanger = false
			}
			if err := r.SendMessageToWebSocket(dto.NewMessage(Data, s)); err != nil {
				return
			}
			// 录屏
			if r.isRecording {
				_ = r.nextTerminal.Recorder.WriteData(s)
			}
			fmt.Println("read =====:", s)
			// 监控
			SendObData(r.sessionId, s)
			r.buf.Reset()
		case data := <-r.dataChan:
			if data != utf8.RuneError {
				p := make([]byte, utf8.RuneLen(data))
				utf8.EncodeRune(p, data)
				r.buf.Write(p)
			} else {
				r.buf.Write([]byte("@"))
			}
		}
	}
}

func (r *TermHandler) WriteCancel() error {
	r.isDanger = true
	_, err := r.nextTerminal.Write([]byte{3})
	return err
}
func (r *TermHandler) Write(input []byte) error {
	// 正常的字符输入
	_, err := r.nextTerminal.Write(input)
	return err
}

func (r *TermHandler) WindowChange(h int, w int) error {
	return r.nextTerminal.WindowChange(h, w)
}

func (r *TermHandler) SendRequest() error {
	_, _, err := r.nextTerminal.SshClient.Conn.SendRequest("helloworld1024@foxmail.com", true, nil)
	return err
}

func (r *TermHandler) SendMessageToWebSocket(msg dto.Message) error {
	if r.webSocket == nil {
		return nil
	}
	defer r.mutex.Unlock()
	r.mutex.Lock()
	message := []byte(msg.ToString())
	return r.webSocket.WriteMessage(websocket.TextMessage, message)
}

func SendObData(sessionId, s string) {
	nextSession := session.GlobalSessionManager.GetById(sessionId)
	if nextSession != nil && nextSession.Observer != nil {
		nextSession.Observer.Range(func(key string, ob *session.Session) {
			_ = ob.WriteMessage(dto.NewMessage(Data, s))
		})
	}
}
