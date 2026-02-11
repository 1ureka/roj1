package webrtc

import (
	"context"

	"github.com/1ureka/1ureka.net.p2p/internal/protocol"
	"github.com/pion/webrtc/v4"
)

const (
	HighWaterMark = 256 * 1024 // pause sending when bufferedAmount exceeds this
	LowWaterMark  = 64 * 1024  // resume sending when bufferedAmount drops below this
)

// DataChannel 封裝 pion DataChannel，內聚背壓控制與封包編解碼。
type DataChannel struct {
	raw       *webrtc.DataChannel
	sendReady chan struct{}
}

// NewDataChannel 將 pion DC 包裝為 DataChannel 並初始化背壓機制。
func NewDataChannel(raw *webrtc.DataChannel) *DataChannel {
	ch := &DataChannel{
		raw:       raw,
		sendReady: make(chan struct{}, 1),
	}

	raw.SetBufferedAmountLowThreshold(uint64(LowWaterMark))
	raw.OnBufferedAmountLow(func() {
		select {
		case ch.sendReady <- struct{}{}:
		default:
		}
	})

	return ch
}

// sendPacket 編碼並傳送封包；於背壓時阻塞直到緩衝區低於水位或 ctx 取消。
func (c *DataChannel) sendPacket(ctx context.Context, typ uint8, socketID, seqNum uint32, payload []byte) error {
	if c.raw.BufferedAmount() > uint64(HighWaterMark) {
		select {
		case <-c.sendReady:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	data := protocol.Encode(&protocol.Packet{
		Type:     typ,
		SocketID: socketID,
		SeqNum:   seqNum,
		Payload:  payload,
	})

	return c.raw.Send(data)
}

// SendConnect 發送 CONNECT 封包，於背壓時阻塞直到緩衝區低於水位或 ctx 取消。
func (c *DataChannel) SendConnect(ctx context.Context, socketID uint32, seqNum uint32) error {
	return c.sendPacket(ctx, protocol.TypeConnect, socketID, seqNum, nil)
}

// SendClose 發送 CLOSE 封包，於背壓時阻塞直到緩衝區低於水位或 ctx 取消。
func (c *DataChannel) SendClose(ctx context.Context, socketID uint32, seqNum uint32) error {
	return c.sendPacket(ctx, protocol.TypeClose, socketID, seqNum, nil)
}

// SendData 發送 DATA 封包，於背壓時阻塞直到緩衝區低於水位或 ctx 取消。
func (c *DataChannel) SendData(ctx context.Context, socketID uint32, seqNum uint32, payload []byte) error {
	return c.sendPacket(ctx, protocol.TypeData, socketID, seqNum, payload)
}

// OnPacket 註冊封包接收回呼（已自動完成 Decode）。
func (c *DataChannel) OnPacket(fn func(*protocol.Packet, error)) {
	c.raw.OnMessage(func(msg webrtc.DataChannelMessage) {
		pkt, err := protocol.Decode(msg.Data)
		fn(pkt, err)
	})
}

// OnOpen / OnClose / Raw 直接代理底層方法。
func (c *DataChannel) OnOpen(fn func())         { c.raw.OnOpen(fn) }
func (c *DataChannel) OnClose(fn func())        { c.raw.OnClose(fn) }
func (c *DataChannel) Raw() *webrtc.DataChannel { return c.raw }
