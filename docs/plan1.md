# Plan：P2P TCP 隧道 CLI 工具

> 透過 WebRTC DataChannel 將遠端 TCP 服務穿透到本地，使用 Go 實作。

---

## 一、專案概述

本工具是一個 CLI 程式，讓兩台無法直接互通的機器透過 WebRTC DataChannel 建立 P2P 隧道，將 host 端的 TCP 服務（任意 port）穿透到 client 端的本地 port。

**核心場景**：host 有一個跑在 `localhost:8080` 的服務，client 想在自己的 `localhost:3000` 存取它 — 中間不經過任何中繼伺服器（signaling 階段除外）。

---

## 二、技術棧

| 元件 | 技術 | 說明 |
|---|---|---|
| 語言 | Go | goroutine + channel + select 完美契合 per-connection 並發模型 |
| WebRTC | [pion/webrtc](https://github.com/pion/webrtc) | 純 Go 實作，無 CGo 依賴，支援 DataChannel ordered/unordered 控制 |
| Signaling | WebSocket | host 建立 WS server，client 連入；僅用於交換 SDP/ICE，完成後即關閉 |
| TCP | Go `net` 標準庫 | host 端 `net.Dial` 連線目標服務；client 端 `net.Listen` 建立虛擬服務 |
| 部署 | 單一二進位檔 | `go build` 產出零依賴的靜態連結執行檔，跨平台編譯 |

---

## 三、CLI 使用流程

### Phase 1：角色選擇

```
? 請選擇角色：
  > Host（提供服務的一方）
    Client（存取服務的一方）
```

### Phase 2：參數輸入

**Host：**

1. 填寫要轉發的 target port（host 上實際執行的服務 port）
2. 程式啟動 WS server，印出 WS port 與隨機生成的 PIN 碼
3. 提示使用者可用 VS Code Port Forwarding 將 WS server 暴露至公網

**Client：**

1. 填寫 WS server URL（placeholder 提示格式：`wss://***.asse.devtunnels.ms/ws?pin=1234`）
2. 填寫虛擬服務要監聽的本地 port

### Phase 3：Signaling（WebSocket）

1. Client 連上 Host 的 WS server（驗證 PIN 碼）
2. Host 建立 `RTCPeerConnection`，建立 DataChannel（**無序、不可靠**），產生 SDP Offer
3. 雙方透過 WS 交換 SDP Offer/Answer 與 ICE Candidates
4. WebRTC 連線建立完成

### Phase 4：WS 清理

WebRTC DataChannel 一旦 open，立即關閉 WS 連線並釋放相關資源。Signaling 使命已完成。

### Phase 5：資料轉發（核心）

進入穩態，所有 TCP 流量透過 DataChannel 雙向轉發。

### Phase 6：結束

使用者按 `Ctrl+C` 或 DataChannel 斷開 → 關閉所有 TCP 連線 → 所有 goroutine 退出 → 程式結束。

---

## 四、核心架構決策

### 4.1 單一無序 DataChannel

在一個完整的 CLI session 中，**只存在一個 DataChannel，且設為無序（unordered）**。

**理由：**

Client 的虛擬服務在接受到本地應用的 TCP 連線後，會立即完成握手（無法推遲），然後幾乎同時送出 `CONNECT` 和後續的 `DATA`。即便 DataChannel 設為 Ordered，由於 SCTP 的排隊行為，`DATA` 仍有機會比 `CONNECT` 先被 host 接收。既然亂序不可避免，不如從設計之初就擁抱它：

1. **效能**：避免 SCTP 層 head-of-line blocking — 不同 socketID 之間的封包不會互相卡住
2. **設計一致性**：所有封包一律被視為可能亂序到達，消除灰色地帶
3. **簡化心智模型**：不會有人誤以為「前面的封包一定先到」而寫出脆弱邏輯

### 4.2 Per-SocketID Goroutine 模型

**不集中管理狀態，讓每個 socketID 擁有自己的 goroutine。**

分發器從 DataChannel 讀取封包，根據 socketID 路由到對應 goroutine 的 channel：

```
DataChannel → 分發器 goroutine → socketID-A 的 channel → goroutine-A
                                → socketID-B 的 channel → goroutine-B
                                → ...
```

每個 goroutine 獨佔自身的：

- TCP 連線
- 重組器（Reassembler）
- 序號產生器
- 暫存緩衝區

**無鎖**。所有狀態都是 goroutine-local 的，天生安全。Go goroutine 初始堆疊僅 2–8 KB，即便數千個活躍連線也不構成壓力。

### 4.3 顯式讀寫迴圈（非 `io.Copy`）

本專案的資料路徑**不是**「串流對串流」的透傳，而是一個在無序 DataChannel 上多工多個邏輯 TCP 連線的封包協議。`io.Copy` 的前提 — 「讀出什麼就寫入什麼」 — 在此完全不成立。

每個方向都有中間處理層：

| 方向 | 讀取端 | 中間處理 | 寫入端 |
|---|---|---|---|
| TCP → DataChannel | `net.Conn.Read`（串流） | 封裝：加 Type + SocketID + SeqNum | `DataChannel.Send`（離散訊息） |
| DataChannel → TCP | Channel 接收（離散封包、可能亂序） | 重組：按 SeqNum 排序、去標頭 | `net.Conn.Write`（串流） |

因此採用顯式迴圈搭配 `select`，獲得 `io.Copy` 無法提供的能力：

1. **控制分塊大小** — 根據 DataChannel MTU / 效能特性決定，而非被 `io.Copy` 的 32 KB 內部 buffer 綁定
2. **`select` 多路複用** — 同時監聽 inbox channel、TCP 讀取、`ctx.Done()`，`io.Copy` 無法參與 channel 操作
3. **生命週期管理** — 在 TCP Read error 時主動發送 `CLOSE` 封包，而非默默退出

---

## 五、封包協議

### 5.1 封包格式

```
┌──────────┬──────────────┬──────────────┬───────────────┐
│ Type (1B)│ SocketID (4B)│ SeqNum (4B)  │ Payload (var) │
└──────────┴──────────────┴──────────────┴───────────────┘
```

| 欄位 | 大小 | 說明 |
|---|---|---|
| Type | 1 byte | `0x01` CONNECT, `0x02` DATA, `0x03` CLOSE |
| SocketID | 4 bytes | 由 4-tuple (srcIP, srcPort, dstIP, dstPort) 雜湊壓縮而成，僅作識別用途，不須可逆 |
| SeqNum | 4 bytes | Per-socketID 遞增序號，用於重組與因果判斷 |
| Payload | 可變 | 僅 `DATA` 類型攜帶，內含原始 TCP 資料 |

### 5.2 封包類型

**`CONNECT`**：Client 虛擬服務收到新 TCP 連線時送出。告知 host 為此 socketID 建立對應的 TCP 連線到目標服務。

**`DATA`**：攜帶 TCP 資料。Client 不等待 `CONNECT` 的確認就開始送 `DATA`（因為 TCP 握手已在本地完成）。

**`CLOSE`**：TCP 連線結束（正常或異常）。通知對端關閉對應的 socketID 資源。

### 5.3 重組器（Reassembler）

由於無序 DataChannel，接收端的每個 socketID goroutine 內建一個重組器：

- 維護 `expectedSeq`：下一個期望的序號
- `seq == expectedSeq`：立即交付，`expectedSeq++`，並檢查緩衝區中是否有可連鎖交付的後續封包
- `seq > expectedSeq`：存入有序緩衝區（min-heap 或 sorted map），等待缺口填補
- 重組器是 goroutine-local 的，不需要鎖

---

## 六、Host 端詳細設計

### 6.1 分發器

單一 goroutine，持續從 DataChannel 讀取封包：

```go
for {
    pkt := readFromDataChannel()
    ch, exists := routeTable[pkt.SocketID]
    if !exists {
        ch = make(chan Packet, bufferSize)
        routeTable[pkt.SocketID] = ch
        go hostSocketHandler(pkt.SocketID, ch)
    }
    ch <- pkt
}
```

首次見到某 socketID 時，建立 channel 並啟動 handler goroutine。

### 6.2 Per-SocketID Handler

```go
func hostSocketHandler(ctx context.Context, id SocketID, inbox <-chan Packet, dc *webrtc.DataChannel, targetAddr string) {
    var tcpConn net.Conn
    var pending []Packet
    reasm := NewReassembler()
    seq := uint32(0)

    for {
        select {
        case pkt, ok := <-inbox:
            if !ok { return }
            for _, delivered := range reasm.Feed(pkt) {
                switch delivered.Type {
                case CONNECT:
                    conn, err := net.Dial("tcp", targetAddr)
                    if err != nil {
                        sendPacket(dc, CLOSE, id, nextSeq(&seq), nil)
                        return
                    }
                    tcpConn = conn
                    tcpReadCh = startTCPReader(ctx, tcpConn)
                    // flush pending
                    for _, p := range pending {
                        tcpConn.Write(p.Payload)
                    }
                    pending = nil

                case DATA:
                    if tcpConn == nil {
                        pending = append(pending, delivered)
                    } else {
                        tcpConn.Write(delivered.Payload)
                    }

                case CLOSE:
                    if tcpConn != nil {
                        tcpConn.Close()
                    }
                    return
                }
            }

        case data := <-tcpReadCh:
            // TCP 回應 → 封裝成 DATA 封包送回 client
            sendPacket(dc, DATA, id, nextSeq(&seq), data)

        case <-ctx.Done():
            if tcpConn != nil { tcpConn.Close() }
            return
        }
    }
}
```

### 6.3 邊界情況處理

| 情境 | 處理方式 |
|---|---|
| `DATA` 先於 `CONNECT` 到達 | 重組器按序號排列後，`CONNECT` 會先被交付；若因亂序 `DATA` 更早到達，goroutine 將其放入 `pending` 佇列 |
| `CLOSE` 先於 `CONNECT` 到達（序號更晚，只是先到） | 重組器確保按因果序號交付。`CONNECT` 會先被處理，連線建立後接著處理 `CLOSE`，關閉連線 |
| TCP 連線失敗 | 送回 `CLOSE` 封包給 client，goroutine 結束 |
| `CLOSE` 到達但無 TCP 連線 | 忽略，記錄日誌，goroutine 結束 |
| `DATA` 到達但無 TCP 連線 | 忽略，記錄日誌 |

---

## 七、Client 端詳細設計

### 7.1 虛擬服務

監聽使用者指定的本地 port，接受來自本地應用的 TCP 連線：

```go
listener, _ := net.Listen("tcp", fmt.Sprintf(":%d", localPort))
for {
    conn, _ := listener.Accept()
    socketID := hash4Tuple(conn)
    go clientSocketHandler(ctx, socketID, conn, dc)
}
```

### 7.2 Per-SocketID Handler

```go
func clientSocketHandler(ctx context.Context, id SocketID, tcpConn net.Conn, dc *webrtc.DataChannel) {
    seq := uint32(0)
    reasm := NewReassembler()
    inbox := registerSocket(id) // 向分發器註冊，取得 inbox channel

    // 立即送出 CONNECT — 不等待
    sendPacket(dc, CONNECT, id, nextSeq(&seq), nil)

    // 啟動 TCP 讀取 goroutine
    tcpReadCh := startTCPReader(ctx, tcpConn)

    for {
        select {
        case pkt, ok := <-inbox:
            if !ok { return }
            for _, delivered := range reasm.Feed(pkt) {
                switch delivered.Type {
                case DATA:
                    tcpConn.Write(delivered.Payload)
                case CLOSE:
                    tcpConn.Close()
                    return
                }
            }

        case data, ok := <-tcpReadCh:
            if !ok {
                // TCP 連線結束
                sendPacket(dc, CLOSE, id, nextSeq(&seq), nil)
                return
            }
            sendPacket(dc, DATA, id, nextSeq(&seq), data)

        case <-ctx.Done():
            tcpConn.Close()
            return
        }
    }
}
```

### 7.3 SocketID 產生

由 4-tuple（srcIP, srcPort, dstIP, dstPort）雜湊壓縮為 4 bytes。在單一 session 的連線量級下（最多數千），碰撞率極低。僅作識別用途，不須可逆。

### 7.4 Client 端也需要分發器

Client 也有一個分發器 goroutine 從 DataChannel 讀取 host 的回應封包，根據 socketID 路由到對應的 client handler goroutine 的 inbox channel。結構與 host 端的分發器對稱。

---

## 八、資料流總覽

### 正向（Client App → Host 目標服務）

```
[Client App]
     │ TCP connect
     ▼
[虛擬服務 Listener]
     │ Accept → 產生 socketID → 啟動 goroutine
     ▼
[Client Handler Goroutine]
     │ TCP Read → 封裝 (Type + SocketID + SeqNum + Payload)
     ▼
[DataChannel.Send] ───WebRTC P2P──→ [DataChannel.OnMessage]
                                          │
                                          ▼
                                    [Host 分發器]
                                          │ 路由到對應 channel
                                          ▼
                                    [Host Handler Goroutine]
                                          │ 重組 → TCP Write
                                          ▼
                                    [Host 目標服務]
```

### 反向（Host 目標服務 → Client App）

反向路徑完全對稱，Host handler 的 TCP Read → 封裝 → DataChannel → Client 分發器 → Client handler → TCP Write → Client App。

---

## 九、Per-SocketID Goroutine 內部結構

每個 handler goroutine（無論 host 或 client）的內部結構是一致的：

```
                ┌──────────────────────────────────┐
                │     Per-SocketID Goroutine        │
                │                                    │
  inbox ──────→ │  [Reassembler]                     │
  (channel)     │       │                            │
                │       ▼                            │
                │  按序交付的封包                     │
                │       │                            │
                │       ├── CONNECT → net.Dial / 略  │
                │       ├── DATA    → tcpConn.Write  │
                │       └── CLOSE   → tcpConn.Close  │
                │                                    │
  tcpReadCh ──→ │  TCP Read 結果                     │
  (channel)     │       │                            │
                │       ▼                            │
                │  封裝 → DataChannel.Send            │
                │                                    │
  ctx.Done ───→ │  清理並退出                         │
                │                                    │
                └──────────────────────────────────┘
```

三個事件源透過 `select` 多路複用，不需要鎖，不需要 `io.Copy`。

---

## 十、Signaling 階段設計

### 10.1 WS Server（Host 端）

Host 啟動 WS server 並產生 4–6 位隨機 PIN。PIN 作為 query parameter 附在 URL 上（`ws://host:port/ws?pin=XXXX`），client 連入時驗證。

**為什麼用 WS + VS Code Port Forwarding**：

- Host 通常在 NAT 後面，WS server 跑在 localhost
- VS Code 的 Port Forwarding（基於 Dev Tunnels）能將 localhost port 暴露為公網 `wss://` URL
- Client 直接連這個 URL，無需架設額外的 signaling 伺服器

### 10.2 Signaling 流程

```
Host                                  Client
 │                                      │
 │◄──── WS connect (驗證 PIN) ─────────│
 │                                      │
 │  建立 PeerConnection                 │
 │  建立 DataChannel (unordered)        │
 │  產生 SDP Offer                      │
 │                                      │
 │────── SDP Offer ────────────────────►│
 │                                      │  建立 PeerConnection
 │                                      │  SetRemoteDescription
 │                                      │  產生 SDP Answer
 │◄───── SDP Answer ───────────────────│
 │                                      │
 │◄────► ICE Candidates 交換 ─────────►│
 │                                      │
 │        WebRTC DataChannel Open       │
 │                                      │
 │──── WS 關閉 ────────────────────────►│
 │                                      │
```

### 10.3 WS 關閉

DataChannel open 後，雙方關閉 WS 連線。此後所有通訊都走 P2P DataChannel，不再有中繼/伺服器參與。

---

## 十一、生命週期管理

### Context 樹

```
rootCtx (Ctrl+C 觸發取消)
  └── dataChannelCtx (DataChannel 關閉時取消)
        ├── socketHandler-A ctx
        ├── socketHandler-B ctx
        └── ...
```

### 清理順序

1. `Ctrl+C` 或 DataChannel 斷開 → `dataChannelCtx` 取消
2. 所有 per-socketID goroutine 收到 `ctx.Done()`，各自關閉 TCP 連線並退出
3. 分發器 goroutine 退出
4. 虛擬服務 Listener 關閉（client 端）
5. PeerConnection 關閉
6. 程式結束

---

## 十二、技術決策摘要

| 決策 | 選擇 | 理由 |
|---|---|---|
| DataChannel 有序/無序 | **無序** | 亂序不可避免（CONNECT/DATA 幾乎同時送出），擁抱無序消除灰色地帶並獲得效能提升 |
| 並發模型 | **Per-socketID goroutine** | 將 O(N×M) 狀態空間分解為 N 個 O(M)，每個可獨立推理，無鎖 |
| 資料轉發方式 | **顯式讀寫迴圈 + `select`** | 協議問題（封裝/解封裝/重組/多工），不是管道問題；`io.Copy` 不適用 |
| Signaling 傳輸 | **WebSocket + VS Code Port Forwarding** | 零基礎設施成本，host 不需要公網 IP |
| WebRTC 函式庫 | **pion/webrtc** | 純 Go、無 CGo、功能完整、社群成熟 |
| 語言 | **Go** | goroutine + channel + select 是此架構最自然的表達；單一二進位檔利於 CLI 分發 |
