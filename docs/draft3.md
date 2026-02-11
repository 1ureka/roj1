# `io.Copy` 適用性分析：為什麼傳統的串流串接在此行不通

> 針對 `draft2.md` 中第 3.4 節「`io.Copy` 配合 goroutine 可以極簡地實現全雙工資料轉發」之論述的深度反思

---

## 一、`io.Copy` 的典型使用場景

在傳統的 TCP 代理或隧道中，`io.Copy` 是慣用手法：

```go
// 經典 TCP proxy — 兩行搞定雙向轉發
go io.Copy(upstream, downstream) // client → server
go io.Copy(downstream, upstream) // server → client
```

這之所以可行，是因為兩端都是**純位元組串流**，且兩者之間是**一對一的直通管道**。`io.Copy` 內部就是一個簡單的迴圈：

```go
// io.Copy 的本質（簡化）
buf := make([]byte, 32*1024)
for {
    n, readErr := src.Read(buf)
    if n > 0 {
        _, writeErr := dst.Write(buf[:n])
        // ...
    }
    // ...
}
```

它對來源和目的地的唯一要求是：**src 實作 `io.Reader`，dst 實作 `io.Writer`** — 不關心內容、不做任何轉換、不維護狀態。

---

## 二、本專案的資料路徑根本不是「串流對串流」

讓我們畫出本專案中，單一 socketID 的完整資料路徑：

### 方向一：TCP → DataChannel

```
TCP 連線 ──Read──→ 原始位元組
                      ↓
               [ 封裝成封包 ]
               加入 Type (DATA)
               加入 SocketID
               加入 SeqNum (遞增)
                      ↓
              已封裝的二進位訊息
                      ↓
         DataChannel.Send(message)
```

### 方向二：DataChannel → TCP

```
DataChannel 收到訊息
        ↓
  [ 分發器路由 ] ← 根據 SocketID 分到對應 goroutine
        ↓
  goroutine 的 inbox channel
        ↓
  [ 重組器 ] ← 可能亂序，需按 SeqNum 排列
        ↓
  按序交付的 Payload
        ↓
  TCP 連線 ──Write──→ 目標服務
```

兩個方向都**不是**簡單的位元組透傳，而是：

| | TCP → DataChannel | DataChannel → TCP |
|---|---|---|
| 讀取端 | 純位元組串流 (`net.Conn.Read`) | 離散的封包訊息（經 channel 路由、可能亂序） |
| 中間處理 | 封裝：加標頭、加序號 | 重組：排序、去標頭、合併 |
| 寫入端 | 離散的訊息傳送 (`DataChannel.Send`) | 純位元組串流 (`net.Conn.Write`) |

**`io.Copy` 假設的前提 — 「讀出什麼就寫入什麼」 — 在這裡完全不成立。** 兩端之間插了一整層封裝/解封裝的邏輯。

---

## 三、就算強行用 Adapter 包裝，也行不通

有人可能會想：Go 的介面組合這麼靈活，我們可以寫 adapter 來包裝，讓 `io.Copy` 重新適用。讓我們逐一檢驗。

### 嘗試 A：包裝 DataChannel 為 `io.Writer`

```go
type dcWriter struct {
    dc       *webrtc.DataChannel
    socketID uint32
    seq      *uint32
}

func (w *dcWriter) Write(p []byte) (int, error) {
    pkt := frame(DATA, w.socketID, atomic.AddUint32(w.seq, 1), p)
    return len(p), w.dc.Send(pkt)
}

// 然後
go io.Copy(dcWriter, tcpConn) // TCP → DataChannel
```

**表面上可行，但有致命問題：**

1. **分塊邊界不可控**：`io.Copy` 使用內部 32 KB buffer，它決定每次 `Read` 多少 bytes。這意味著你的封包大小完全被 `io.Copy` 的內部實作決定，而不是由你根據 DataChannel 的 MTU 或效能特性來控制。

2. **無法中斷**：`io.Copy` 在 `tcpConn.Read` 上阻塞——它是一個封閉的迴圈，不接受外部訊號。如果對端送了 `CLOSE`，你的 goroutine 需要立刻停止從 TCP 讀取，但 `io.Copy` 不知道這件事。唯一的辦法是從外面把 `tcpConn` 強行 Close，迴圈才會因 Read error 退出 — 但這不夠優雅：它會回報一個人為製造的 error，混淆了「真正的網路錯誤」和「正常的生命週期結束」。

3. **與 `select` 互斥**：`io.Copy` 獨佔一個 goroutine。但在 draft2 的設計中，per-socketID 的 goroutine 需要用 `select` 同時監聽 inbox channel *和* TCP 讀取。`io.Copy` 無法參與 `select`，因為它不是 channel 操作。

### 嘗試 B：包裝重組器為 `io.Reader`

```go
type reassemblerReader struct {
    inbox <-chan Packet
    reasm *Reassembler
    buf   []byte
}

func (r *reassemblerReader) Read(p []byte) (int, error) {
    // 從 inbox 讀封包，餵進重組器，交付後拷貝到 p
    // ...
}

// 然後
go io.Copy(tcpConn, reassemblerReader) // DataChannel → TCP
```

**問題同樣嚴重：**

1. **阻塞語意不匹配**：`io.Reader.Read` 的契約是「至少讀 1 byte 或回傳 error」。但重組器可能已收到封包但還在等缺口（例如收到 seq 3、5，等 seq 4）。此時 `Read` 應該阻塞？還是回傳 0 bytes？回傳 0 bytes 違反 `io.Reader` 契約；阻塞則沒問題，但阻塞點是在 channel 接收上，又回到了「無法 `select`」的問題。

2. **生命週期訊號被吞掉**：`CLOSE` 封包和 `DATA` 封包走同一個 inbox channel。如果我們把 inbox 包成 `io.Reader`，`CLOSE` 封包要怎麼處理？回傳 `io.EOF`？但 `CLOSE` 可能只是關閉一個方向，或者攜帶了需要特殊處理的語意。把控制訊號和資料訊號混在同一個 `Read` 介面裡，會讓語意變得模糊。

3. **錯誤丟失**：重組器發現序號不連續（gap 超過閾值、疑似丟包）時應回報什麼？`io.Reader` 只有 `error` 一個出口，沒有辦法區分「暫時的亂序」和「真正的丟包」和「正常結束」。

---

## 四、根本原因：這不是管道問題，而是協議問題

`io.Copy` 解決的是**管道問題** — 把位元組從 A 搬到 B。

本專案面對的是**協議問題** — 在一條共享的、無序的 DataChannel 上，多工多個邏輯 TCP 連線，每個連線有自己的生命週期（CONNECT / DATA / CLOSE）和序號空間。

| 特徵 | 管道（`io.Copy` 適用） | 協議（本專案） |
|---|---|---|
| 中間層 | 無，透傳 | 有封裝/解封裝 |
| 訊息邊界 | 不關心 | 必須維護（封包格式） |
| 順序保證 | 來源端保證 | 需自行重組 |
| 連線對應 | 1:1 | N:1 多工 |
| 控制訊號 | EOF / error | CONNECT / CLOSE 封包 |

試圖把「協議處理」硬塞進 `io.Reader` / `io.Writer` 的介面中，本質上是在用一個**無狀態的串流抽象**去表達一個**有狀態的封包協議**。即使技術上做得到，也只是把複雜度藏進了 adapter 裡，反而讓程式碼更難理解。

---

## 五、正確的做法：顯式的讀寫迴圈

### TCP → DataChannel 方向

```go
func tcpToDataChannel(ctx context.Context, tcpConn net.Conn, dc *webrtc.DataChannel, socketID uint32, seq *uint32) {
    buf := make([]byte, maxPayloadSize) // 你控制分塊大小
    for {
        select {
        case <-ctx.Done():
            return
        default:
        }

        tcpConn.SetReadDeadline(time.Now().Add(readTimeout))
        n, err := tcpConn.Read(buf)
        if n > 0 {
            pkt := frame(DATA, socketID, nextSeq(seq), buf[:n])
            dc.Send(pkt)
        }
        if err != nil {
            if !isTimeout(err) {
                sendClose(dc, socketID, nextSeq(seq))
                return
            }
            // timeout → 回到 select 檢查 ctx
        }
    }
}
```

注意這個迴圈做了 `io.Copy` 做不到的三件事：

1. **你控制 `buf` 的大小** — 而不是被 `io.Copy` 的 32 KB 預設值綁定
2. **你有機會檢查 `ctx.Done()`** — 透過 `SetReadDeadline` + timeout 模式，讓 blocking Read 可以被中斷
3. **你在 Read error 時發送 `CLOSE` 封包** — 而不只是默默退出

### DataChannel → TCP 方向

這部分不需要獨立的迴圈，因為它已經融合在 per-socketID goroutine 的 `select` 主迴圈中：

```go
func handleSocket(ctx context.Context, inbox <-chan Packet, tcpConn net.Conn, dc *webrtc.DataChannel) {
    reasm := NewReassembler()
    tcpReadCh := startTCPReader(ctx, tcpConn) // 另一個 goroutine 讀 TCP

    for {
        select {
        case pkt, ok := <-inbox:
            if !ok { return }
            switch pkt.Type {
            case DATA:
                for _, payload := range reasm.Feed(pkt) { // 按序交付
                    tcpConn.Write(payload)
                }
            case CLOSE:
                tcpConn.Close()
                return
            }
        case data := <-tcpReadCh:
            // 反向：TCP 資料封裝後送回 DataChannel
        case <-ctx.Done():
            return
        }
    }
}
```

此處 `inbox` 和 `tcpReadCh` 的多路複用是 `io.Copy` 永遠無法表達的。

---

## 六、`io.Copy` 僅在一處可能有用（但也不必要）

有一個極其邊緣的場景：**重組器已經確保了封包按序交付之後**，將 payload 寫入 TCP：

```go
for _, payload := range reasm.Feed(pkt) {
    tcpConn.Write(payload)
}
```

理論上，你可以把重組器的輸出端包成 `io.Reader`，然後用 `io.Copy(tcpConn, reader)` 來寫。但這只是把一個 `for + Write` 換成了一個 `io.Copy` — 沒有任何語意上的好處，反而增加了一層間接。

**結論：`io.Copy` 在本專案中沒有合理的使用場景。**

---

## 七、總結

| 問題 | 回答 |
|---|---|
| `io.Copy` 在此架構中有必要嗎？ | **否。** 它解決的是管道問題，本專案面對的是協議問題。 |
| `io.Copy` 在此架構中能用嗎？ | **技術上可以硬包 adapter，但會引入更多問題**（分塊不可控、無法 `select`、控制訊號混淆）。 |
| 正確的替代方案是什麼？ | **顯式的讀寫迴圈**，搭配 `select` 實現多路複用和生命週期管理。 |

draft2.md 的 3.4 節提到的「`io.Copy` 配合 goroutine 可以極簡地實現全雙工資料轉發」是在描述 Go 標準庫在**傳統代理場景**下的能力，但在本專案的多工封包協議架構中，這項能力並不適用。

**Go 語言真正的武器不是 `io.Copy`，而是 goroutine + channel + `select`** — 它們讓你可以寫出清晰的顯式迴圈，而不需要依賴任何「魔法串接」。在一個自訂協議的世界裡，控制權越明確越好。
