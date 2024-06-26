// Package p2p
// @Description: 最底层的 Peer，它接收来自 node.Peer 的消息，序列化后发送到数据流，并且接收远端的数据，反序列化后传递给 node.Peer
// @Description: 这里可以作为一个黑盒使用，类似 socket 中的 send，recv，不进行详细的注释
package p2p

import (
	"bufio"
	"context"
	"encoding/base64"
	"github.com/chain-lab/go-norn/metrics"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	log "github.com/sirupsen/logrus"
	karmem "karmem.org/golang"
	"sync"
	"time"
)

const (
	pingInterval    = 15 * time.Second
	messageQueueCap = 5000
	//bufferSize   = 50 * 1024 * 1024
)

var (
	contextOnce sync.Once
	peerContext context.Context
	//writerPool  = sync.Pool{New: func() any { return karmem.NewWriter(1024) }}
)

type Peer struct {
	peerID peer.ID

	rw        *bufio.ReadWriter
	wg        sync.WaitGroup
	msgQueue  chan *Message
	sendQueue chan *Message

	wLock sync.RWMutex
	rLock sync.RWMutex

	stopped bool
}

func NewPeer(id peer.ID, s *network.Stream, msgQueue chan *Message) (*Peer, error) {
	p := Peer{
		peerID: id,
		//rw:       bufio.NewReadWriter(bufio.NewReaderSize(*s, bufferSize), bufio.NewWriterSize(*s, bufferSize)),
		rw:        bufio.NewReadWriter(bufio.NewReader(*s), bufio.NewWriter(*s)),
		msgQueue:  msgQueue,
		sendQueue: make(chan *Message, messageQueueCap),
		stopped:   false,
	}

	metrics.RoutineCreateCounterObserve(13)
	go p.Run()

	return &p, nil
}

func (p *Peer) Stopped() bool {
	return p.stopped
}

func (p *Peer) Run() {
	var (
		readErr = make(chan error, 1)
	)

	log.WithField("peer", p.peerID).Infoln("Start run peer instance.")

	p.wg.Add(2)
	metrics.RoutineCreateCounterObserve(12)
	//go p.pingLoop()

	// 不可多协程并发写，存在问题
	go p.readLoop(readErr)
	go p.writeLoop()
	return
}

func (p *Peer) Id() peer.ID {
	return p.peerID
}

func (p *Peer) pingLoop() {
	ping := time.NewTicker(pingInterval)
	defer p.wg.Done()

	log.Infoln("Start ping loop.")

	for {
		if p.stopped {
			break
		}

		select {
		case <-ping.C:
			log.Debugln("Send ping to peer.")
			p.Send(StatusCodePingMsg, make([]byte, 0))
		}
	}
}

func (p *Peer) readLoop(errc chan<- error) {
	log.Traceln("Start read loop.")

	defer p.wg.Done()
	for {
		if p.stopped {
			break
		}

		log.Traceln("New read loop.")
		dataBytes, err := p.rw.ReadBytes(0xff)
		if err != nil {
			log.WithField("error", err).Debugln("Read bytes error.")
			errc <- err
			return
		}

		if len(dataBytes) == 0 {
			continue
		}
		//log.Infof("Receive byte data %v", dataBytes)
		dataBytes = dataBytes[:len(dataBytes)-1]

		decodedPayload := make([]byte, base64.StdEncoding.DecodedLen(len(dataBytes)))
		l, _ := base64.StdEncoding.Decode(decodedPayload, dataBytes)

		msg := new(Message)
		msg.ReadAsRoot(karmem.NewReader(decodedPayload[:l]))

		now := time.Now()

		// todo: 修改编码为int64
		msg.ReceiveAt = now.UnixMilli()

		p.handle(msg)
	}
}

func (p *Peer) handle(msg *Message) {
	switch {
	case msg.Code == StatusCodePingMsg:
		log.WithField("peer", p.peerID).Traceln("Receive peer ping message.")
		p.Send(StatusCodePongMsg, make([]byte, 0))
		return
	case msg.Code == StatusCodePongMsg:
		return
	default:
		metrics.RecvQueueCountInc()
		p.msgQueue <- msg
	}
	return
}

// Send 方法用于提供一个通用的消息发送接口
func (p *Peer) Send(msgCode StatusCode, payload []byte) {
	msg := Message{
		Code:      msgCode,
		Size:      uint32(len(payload)),
		Payload:   payload,
		ReceiveAt: 0,
	}

	p.sendQueue <- &msg
	metrics.SendQueueCountInc()
}

func (p *Peer) writeLoop() {
	log.Traceln("Start write loop.")
	for {
		if p.stopped {
			break
		}

		select {
		case msg := <-p.sendQueue:
			msgWriter := karmem.NewWriter(1024)

			if _, err := msg.WriteAsRoot(msgWriter); err != nil {
				log.WithField("error", err).Errorln("Encode data failed.")
				break
			}

			msgBytes := msgWriter.Bytes()

			encodedPayload := make([]byte, base64.StdEncoding.EncodedLen(len(msgBytes)))
			base64.StdEncoding.Encode(encodedPayload, msgBytes)
			encodedPayload = append(encodedPayload, 0xff)

			length, err := p.rw.Write(encodedPayload)

			if err != nil {
				log.WithFields(
					log.Fields{
						"error":  err,
						"length": length,
						"code":   msg.Code,
					}).Debugln("Send data to peer errored.")
				p.stopped = true
				log.Debugln("Peer closed.")
				metrics.ConnectedNodeDec()
				break
			}

			// 这里必须强制 Flush， 否则短消息收不到
			p.rw.Flush()
		}
	}
}
