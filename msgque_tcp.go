package antnet

import (
	"bufio"
	"io"
	"net"
	"runtime"
	"sync/atomic"
	"time"
)

type tcpMsgQue struct {
	id        uint32        //唯一标示
	conn      net.Conn      //连接
	listener  net.Listener  //监听
	cwrite    chan *Message //写入通道
	stop      int32         //停止标记
	msgTyp    MsgType       //消息类型
	msgqueTyp MsgQueType    //通道类型

	handler       IMsgHandler //处理者
	parser        IParser
	parserFactory *Parser
	timeout       int //传输超时

	user interface{}
}

var DefMsgQueTimeout int = 180

func (r *tcpMsgQue) SetUser(user interface{}) {
	r.user = user
}

func (r *tcpMsgQue) User() interface{} {
	return r.user
}
func (r *tcpMsgQue) GetHandler() IMsgHandler {
	return r.handler
}
func (r *tcpMsgQue) GetMsgType() MsgType {
	return r.msgTyp
}

func (r *tcpMsgQue) GetMsgQueType() MsgQueType {
	return r.msgqueTyp
}

func (r *tcpMsgQue) Id() uint32 {
	return r.id
}

func (r *tcpMsgQue) SetTimeout(t int) {
	r.timeout = t
}

func (r *tcpMsgQue) GetConnType() ConnType {
	return ConnTypeTcp
}

func (r *tcpMsgQue) Stop() {
	if atomic.CompareAndSwapInt32(&r.stop, 0, 1) {
		if r.cwrite != nil {
			close(r.cwrite)
		}
		if r.listener != nil {
			if tcp, ok := r.listener.(*net.TCPListener); ok {
				tcp.Close()
			}
		}

		r.handler.OnDelMsgQueue(r)
		LogInfo("msgque close id:%d", r.id)

		msgqueMapSync.Lock()
		delete(msgqueMap, r.id)
		msgqueMapSync.Unlock()
	}
}

func (r *tcpMsgQue) LocalAddr() string {
	if r.conn != nil {
		return r.conn.LocalAddr().String()
	} else if r.listener != nil {
		return r.listener.Addr().String()
	}
	return ""
}

func (r *tcpMsgQue) RemoteAddr() string {
	if r.conn != nil {
		return r.conn.RemoteAddr().String()
	}
	return ""
}

func (r *tcpMsgQue) IsStop() bool {
	if r.stop == 0 {
		if IsStop() {
			r.Stop()
		}
	}
	return r.stop == 1
}

func (r *tcpMsgQue) readMsg() {
	headData := make([]byte, MessageHeadSize)
	var data []byte
	var head *MessageHead

	for !r.IsStop() {
		r.conn.SetReadDeadline(time.Now().Add(time.Duration(r.timeout) * time.Second))
		if head == nil {
			_, err := io.ReadFull(r.conn, headData)
			if err != nil {
				if err != io.EOF {
					LogError("msgque read id:%v err:%v", r.id, err)
				} else {
					LogInfo("msgque read close id:%v", r.id)
				}
				break
			}

			if head = NewMessageHead(headData); head == nil {
				break
			}

			if head.Len == 0 {
				msg := &Message{Head: head}
				if r.parser != nil {
					mp, err := r.parser.ParseC2S(msg)
					if err == nil {
						msg.IMsgParser = mp
					} else {
						if r.parser.GetErrType() == ParseErrTypeSendRemind {
							r.Send(r.parser.GetRemindMsg(err, r.msgTyp).CopyTag(msg))
							continue
						} else if r.parser.GetErrType() == ParseErrTypeClose {
							break
						}
					}
				}

				f := r.handler.GetHandlerFunc(msg)
				if f == nil {
					f = r.handler.OnProcessMsg
				}
				if !f(r, msg) {
					break
				}
				head = nil
			} else {
				data = make([]byte, head.Len)
			}
		} else {
			_, err := io.ReadFull(r.conn, data)
			if err != nil {
				if err != io.EOF {
					LogError("msgque read id:%v err:%v", r.id, err)
				} else {
					LogInfo("msgque read close id:%v", r.id)
				}
				break
			}
			msg := &Message{Head: head, Data: data}
			if r.parser != nil {
				mp, err := r.parser.ParseC2S(msg)
				if err == nil {
					msg.IMsgParser = mp
				} else {
					if r.parser.GetErrType() == ParseErrTypeSendRemind {
						r.Send(r.parser.GetRemindMsg(err, r.msgTyp).CopyTag(msg))
						continue
					} else if r.parser.GetErrType() == ParseErrTypeClose {
						break
					}
				}
			}
			f := r.handler.GetHandlerFunc(msg)
			if f == nil {
				f = r.handler.OnProcessMsg
			}
			if !f(r, msg) {
				break
			}

			head = nil
			data = nil
		}
	}
}

func (r *tcpMsgQue) writeMsg() {
	var m *Message
	var head []byte
	writeCount := 0
	for !r.IsStop() || m != nil {
		if m == nil {
			select {
			case m = <-r.cwrite:
				if m != nil {
					head = m.Head.Bytes()
				}
			}
		}
		if m != nil {
			r.conn.SetWriteDeadline(time.Now().Add(time.Duration(r.timeout) * time.Second))
			if writeCount < MessageHeadSize {
				n, err := r.conn.Write(head[writeCount:])
				if err != nil {
					LogError("msgque write id:%v err:%v", r.id, err)
					r.Stop()
					break
				}
				writeCount += n
			}

			if writeCount >= MessageHeadSize && m.Data != nil {
				n, err := r.conn.Write(m.Data[writeCount-MessageHeadSize : int(m.Head.Len)])
				if err == io.EOF {
					LogError("msgque write id:%v err:%v", r.id, err)
					r.Stop()
					break
				}
				writeCount += n
			}

			if writeCount == int(m.Head.Len)+MessageHeadSize {
				writeCount = 0
				m = nil
			}
		}
	}
}

func (r *tcpMsgQue) Send(m *Message) (re bool) {
	if m == nil {
		return
	}
	defer func() {
		if err := recover(); err != nil {
			re = false
		}
	}()

	re = true
	r.cwrite <- m
	return
}

func (r *tcpMsgQue) SendString(str string) (re bool) {
	defer func() {
		if err := recover(); err != nil {
			re = false
		}
	}()

	re = true
	r.cwrite <- &Message{Data: []byte(str)}
	return
}

func (r *tcpMsgQue) SendStringLn(str string) (re bool) {
	return r.SendString(str + "\n")
}

func (r *tcpMsgQue) SendByteStr(str []byte) (re bool) {
	return r.SendString(string(str))
}

func (r *tcpMsgQue) SendByteStrLn(str []byte) (re bool) {
	return r.SendString(string(str) + "\n")
}

func (r *tcpMsgQue) readCmd() {
	reader := bufio.NewReader(r.conn)
	for !r.IsStop() {
		r.conn.SetReadDeadline(time.Now().Add(time.Duration(r.timeout) * time.Second))
		data, err := reader.ReadBytes('\n')
		if err != nil {
			if err != io.EOF {
				LogError("msgque read id:%v err:%v", r.id, err)
			} else {
				LogInfo("msgque read close id:%v", r.id)
			}
			break
		}
		msg := &Message{Data: data}
		if r.parser != nil {
			mp, err := r.parser.ParseC2S(msg)
			if err == nil {
				msg.IMsgParser = mp
			} else {
				if r.parser.GetErrType() == ParseErrTypeSendRemind {
					r.Send(r.parser.GetRemindMsg(err, r.msgTyp))
					continue
				} else if r.parser.GetErrType() == ParseErrTypeClose {
					break
				}
			}
		}
		f := r.handler.GetHandlerFunc(msg)
		if f == nil {
			f = r.handler.OnProcessMsg
		}
		if !f(r, msg) {
			break
		}
	}
}

func (r *tcpMsgQue) writeCmd() {
	var m *Message
	writeCount := 0
	for !r.IsStop() || m != nil {
		if m == nil {
			select {
			case m = <-r.cwrite:
			}
		}
		if m != nil {
			r.conn.SetWriteDeadline(time.Now().Add(time.Duration(r.timeout) * time.Second))
			n, err := r.conn.Write(m.Data[writeCount:])
			if err != nil {
				LogError("msgque write id:%v err:%v", r.id, err)
				r.Stop()
				break
			}
			writeCount += n
			if writeCount == len(m.Data) {
				writeCount = 0
				m = nil
			}
		}
	}
}

func (r *tcpMsgQue) read() {
	defer func() {
		if err := recover(); err != nil {
			LogError("msgque read panic id:%v err:%v", r.id, err.(error))
			buf := make([]byte, 1<<12)
			LogError(string(buf[:runtime.Stack(buf, false)]))
		}
		r.Stop()
	}()

	if r.msgTyp == MsgTypeCmd {
		r.readCmd()
	} else {
		r.readMsg()
	}
}

func (r *tcpMsgQue) write() {
	defer func() {
		if err := recover(); err != nil {
			LogError("msgque write panic id:%v err:%v", r.id, err.(error))
			r.Stop()
		}
	}()
	if r.msgTyp == MsgTypeCmd {
		r.writeCmd()
	} else {
		r.writeMsg()
	}

	if r.conn != nil {
		r.conn.Close()
	}
}

func (r *tcpMsgQue) listen() {
	for !r.IsStop() {
		c, err := r.listener.Accept()
		if err != nil {
			break
		} else {
			Go(func() {
				msgque := newTcpAccept(c, r.msgTyp, r.handler, r.parserFactory)
				LogInfo("process accept for msgque:%d", msgque.id)
				if r.handler.OnNewMsgQue(msgque) {
					Go(func() {
						LogInfo("process read for msgque:%d", msgque.id)
						msgque.read()
						LogInfo("process read end for msgque:%d", msgque.id)
					})
					Go(func() {
						LogInfo("process write for msgque:%d", msgque.id)
						msgque.write()
						LogInfo("process write end for msgque:%d", msgque.id)
					})
				} else {
					msgque.Stop()
				}
				LogInfo("process accept end for msgque:%d", msgque.id)
			})
		}
	}

	r.Stop()
}

func newTcpConn(conn net.Conn, msgtyp MsgType, handler IMsgHandler, parser *Parser) *tcpMsgQue {
	msgque := tcpMsgQue{
		id:        atomic.AddUint32(&msgQueId, 1),
		conn:      conn,
		cwrite:    make(chan *Message, 64),
		msgTyp:    msgtyp,
		handler:   handler,
		timeout:   DefMsgQueTimeout,
		msgqueTyp: MsgQueTypeConn,
	}
	if parser != nil {
		msgque.parser = parser.Get()
	}
	msgqueMapSync.Lock()
	msgqueMap[msgque.id] = &msgque
	msgqueMapSync.Unlock()
	LogInfo("new msgque id:%d from addr:%s", msgque.id, conn.RemoteAddr().String())
	return &msgque
}

func newTcpAccept(conn net.Conn, msgtyp MsgType, handler IMsgHandler, parser *Parser) *tcpMsgQue {
	msgque := tcpMsgQue{
		id:        atomic.AddUint32(&msgQueId, 1),
		conn:      conn,
		cwrite:    make(chan *Message, 64),
		msgTyp:    msgtyp,
		handler:   handler,
		timeout:   DefMsgQueTimeout,
		msgqueTyp: MsgQueTypeAccept,
	}
	if parser != nil {
		msgque.parser = parser.Get()
	}
	msgqueMapSync.Lock()
	msgqueMap[msgque.id] = &msgque
	msgqueMapSync.Unlock()
	LogInfo("new msgque id:%d from addr:%s", msgque.id, conn.RemoteAddr().String())
	return &msgque
}

func newTcpListen(listener net.Listener, msgtyp MsgType, handler IMsgHandler, parser *Parser) *tcpMsgQue {
	msgque := tcpMsgQue{
		id:            atomic.AddUint32(&msgQueId, 1),
		listener:      listener,
		msgTyp:        msgtyp,
		handler:       handler,
		parserFactory: parser,
		msgqueTyp:     MsgQueTypeListen,
	}

	msgqueMapSync.Lock()
	msgqueMap[msgque.id] = &msgque
	msgqueMapSync.Unlock()
	LogInfo("new tcp listen id:%d addr:%s", msgque.id, listener.Addr().String())
	return &msgque
}
