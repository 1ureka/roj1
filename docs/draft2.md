# 計畫前報告：架構決策與 Go 語言優勢分析

> 基於 `future_plan.md` 草案的深度技術分析

---

## 一、如何從根源上馴服 RTC 連線後的狀態複雜度

### 問題本質

草案中描述的 host 端邏輯，本質上是一個 **per-socketID 的有限狀態機 (FSM)**，每個 socketID 都可能面臨：

- `CONNECT` → 建立 TCP 連線
- `DATA` 在 `CONNECT` 之前到達 → 需要緩衝
- `CLOSE` 在 `CONNECT` 之前到達（但序號更晚、只是先到）→ 需要清空並等待新的 `CONNECT`
- `CLOSE` 在已無 TCP 連線時到達 → 忽略但記錄
- `DATA` 在已無 TCP 連線時到達 → 忽略但記錄
- TCP 連線失敗 → 回傳 `CLOSE` 給 client

這些分支會隨著健壯性要求持續膨脹。如果用傳統的「一個大 switch + 共享狀態 + 鎖」來實作，會迅速變成難以維護的怪物。

### 根源性解法：Per-SocketID Goroutine 模型

核心思路：**不要集中管理狀態，讓每個 socketID 擁有自己的執行上下文。**

在 Go 中，這意味著為每個 socketID 啟動一個獨立的 goroutine。這個 goroutine：

- 擁有一個 channel 作為「收件匣」，接收來自 DataChannel 的封包
- 獨佔所有屬於該 socketID 的狀態（TCP 連線、緩衝區、重組器、序號狀態）
- 以簡單的順序迴圈處理訊息 — FSM 退化成線性流程
- **不需要鎖**，因為所有狀態都是 goroutine-local 的

架構上只需要一個「分發器」goroutine 從 DataChannel 讀取封包，根據 socketID 路由到對應的 goroutine：

```
DataChannel → 分發器 goroutine → socketID-A 的 channel → goroutine-A
                                → socketID-B 的 channel → goroutine-B
                                → socketID-C 的 channel → goroutine-C
```

這樣一來，草案中所有的邊界情況都變成了 goroutine 內部的簡單順序邏輯：

| 邊界情況 | 在 Per-Goroutine 模型中的處理 |
|---|---|
| DATA 先於 CONNECT 到達 | Goroutine 在 TCP 連線建立前，自動緩衝到本地佇列 |
| CLOSE 先於 CONNECT 到達（序號更晚） | Goroutine 看到序號後知道該清空狀態，等待下一個 CONNECT |
| TCP 連線失敗 | Goroutine 直接送出 CLOSE 封包，結束自身 |
| CLOSE 在已無 TCP 連線時到達 | Goroutine 簡單忽略並記錄日誌 |
| DATA 在已無 TCP 連線時到達 | Goroutine 簡單忽略並記錄日誌 |

**關鍵洞察**：原本的「狀態爆炸」問題不是因為狀態本身複雜，而是因為所有 socketID 的狀態被攪在一起管理。隔離之後，每個 goroutine 的邏輯都足夠簡單到可以一眼看完。

---

## 二、無序 DataChannel 下的封包設計

### 為什麼選擇無序

草案中有一段關鍵觀察：

> 由於你不可能「推遲握手完成」直到 host 也建立好對應 id 的 socket，因此可以肯定客戶端的應用程式是立刻握手成功，後續的 DATA 立刻開始送。

這意味著**即便 DataChannel 設為 Ordered，DATA 封包仍有機會比 CONNECT 先到達 host**（因為 CONNECT 和 DATA 是從 client 端的應用層幾乎同時送出的）。既然無論如何都得處理亂序，不如從設計之初就擁抱無序：

1. **效能**：無序 DataChannel 避免了 SCTP 層的 head-of-line blocking
2. **設計一致性**：所有封包都被視為可能亂序到達，不存在「有時有序有時無序」的灰色地帶
3. **簡化心智模型**：開發者不會誤以為「前面的封包一定先到」而寫出脆弱的程式碼

> 在一個完整的單個 CLI session，只會有一個 DataChannel 存在，且它無序。

### 封包格式

每個透過 DataChannel 傳輸的封包需要攜帶足夠的元資料：

```
┌──────────┬──────────────┬──────────────┬───────────────┐
│ Type (1B)│ SocketID (4B)│ SeqNum (4B)  │ Payload (var) │
└──────────┴──────────────┴──────────────┴───────────────┘
```

- **Type**：`CONNECT (0x01)`, `DATA (0x02)`, `CLOSE (0x03)`
- **SocketID**：由 4-tuple 壓縮而成的 hash，4 bytes 提供約 42 億個空間，在單 session 的連線數量下碰撞率極低
- **SeqNum**：Per-socketID 的遞增序號，用於重組與判斷因果順序
- **Payload**：僅 DATA 類型使用，攜帶實際的 TCP 資料

### 重組器設計

由於 DataChannel 無序，接收端需要 per-socketID 的重組器：

- 維護一個「期望序號」(`expectedSeq`)
- 到達的封包如果序號 == `expectedSeq`，立即交付並推進
- 到達的封包如果序號 > `expectedSeq`，放入有序緩衝區（min-heap 或 sorted map）等待
- 每次交付後檢查緩衝區是否有連續的後續封包可以一併交付

這個重組器如果放在全域共享，就需要加鎖。但在 per-socketID goroutine 模型下，它只是 goroutine 內的一個局部變數 — 天生安全。

---

## 三、Go 語言在此架構中的獨特優勢

### 3.1 Goroutine：輕量級並發的基石

Go 的 goroutine 初始堆疊僅 2–8 KB，可以輕鬆啟動數萬個。對比：

| 模型 | 每個單位開銷 | 適合的 socketID 數量級 |
|---|---|---|
| OS Thread (C/C++/Java) | ~1 MB stack | 數百 |
| Goroutine (Go) | ~2–8 KB stack | 數萬到數十萬 |
| Async/Callback (Node.js) | 極低 | 理論上無限，但回調地獄 |

在本架構中，每個 socketID 一個 goroutine + 每個 TCP 連線的讀寫各一個 goroutine，即便有上千個同時活躍的連線，也不過幾千個 goroutine — 對 Go runtime 來說輕而易舉。

### 3.2 Channel：天然的狀態隔離與通訊

Go 的 channel 是解決本架構核心問題的利器：

```go
// 分發器
for {
    packet := readFromDataChannel()
    ch, exists := routeTable[packet.SocketID]
    if !exists {
        ch = make(chan Packet, bufferSize)
        routeTable[packet.SocketID] = ch
        go handleSocket(packet.SocketID, ch) // 啟動新 goroutine
    }
    ch <- packet
}
```

```go
// Per-socketID handler
func handleSocket(id SocketID, inbox <-chan Packet) {
    var tcpConn net.Conn
    var pending []Packet

    for pkt := range inbox {
        switch pkt.Type {
        case CONNECT:
            tcpConn = dial(targetPort)
            // flush pending...
        case DATA:
            if tcpConn == nil {
                pending = append(pending, pkt)
            } else {
                reassembleAndForward(tcpConn, pkt)
            }
        case CLOSE:
            tcpConn.Close()
            return
        }
    }
}
```

沒有鎖、沒有共享狀態、沒有回調嵌套。CSP (Communicating Sequential Processes) 模型讓複雜的並發邏輯讀起來像同步程式。

### 3.3 `select` 語句：優雅的多路複用

每個 socketID 的 goroutine 不只要聽 DataChannel 來的封包，還要同時聽 TCP 連線回來的資料：

```go
for {
    select {
    case pkt := <-inbox:
        // 處理來自 DataChannel 的封包
    case data := <-tcpReadCh:
        // TCP 回傳的資料，封裝成 DATA 封包送回 DataChannel
    case <-ctx.Done():
        // 清理並退出
    }
}
```

`select` 語句讓 goroutine 可以同時等待多個事件源，無需 poll、無需 epoll 手動管理、無需 async/await 語法糖。

### 3.4 標準庫的一等公民網路支援

Go 的 `net` 套件提供了簡潔的 TCP 客戶端/伺服器 API：

- **Host 端**：`net.Dial("tcp", addr)` 建立到目標服務的連線
- **Client 端**：`net.Listen("tcp", addr)` 建立虛擬服務的監聽器
- **雙向轉發**：`io.Copy` 配合 goroutine 可以極簡地實現全雙工資料轉發

### 3.5 `pion/webrtc`：成熟的純 Go WebRTC 實作

[pion/webrtc](https://github.com/pion/webrtc) 是目前最成熟的 Go WebRTC 函式庫：

- **純 Go 實作**，不依賴 CGo 或外部 C 函式庫
- 支援 DataChannel、ICE、DTLS、SCTP 完整棧
- 可以精確控制 DataChannel 的 ordered/unordered 設定
- 社群活躍，廣泛用於生產環境

### 3.6 單一二進位檔：CLI 工具的理想部署

Go 編譯出的是靜態連結的單一二進位檔：

- **零依賴部署**：不需要 runtime、不需要 DLL、不需要 `npm install`
- **跨平台編譯**：`GOOS=linux GOARCH=amd64 go build` 一行搞定
- 對於一個 P2P CLI 工具來說，使用者只需下載一個檔案即可使用 — 這是極佳的 DX (Developer Experience)

---

## 四、架構總覽

### 資料流

```
Client App ←TCP→ [虛擬服務 (Client)] ←WebRTC DataChannel→ [分發器 (Host)] ←TCP→ Host 的目標服務
```

具體而言：

1. Client 端的虛擬服務接受 TCP 連線
2. 將 TCP 資料封裝成 `DATA` 封包（附帶 socketID + seqNum），透過 DataChannel 送出
3. Host 端的分發器接收封包，路由到對應的 per-socketID goroutine
4. Goroutine 重組後將資料寫入對應的 TCP 連線，送達 host 的目標服務
5. 目標服務的回應走反向路徑

### 雙端對稱性

Client 端的虛擬服務也需要同樣的 per-socketID 模型，但角色相反：

- 每當虛擬服務接到新的 TCP 連線，產生 socketID，啟動 goroutine
- Goroutine 同時監聽 TCP 讀取和 DataChannel 回傳
- TCP 的資料封裝送出、DataChannel 的資料重組後寫入 TCP

Host 與 Client 的 per-socketID handler 邏輯高度對稱，可以抽象出共用的基礎設施。

### 生命週期

```
[Signaling Phase]          [Data Phase]              [Teardown]
 WS 連線建立        →     WebRTC DataChannel 建立  →  WS 關閉
 SDP/ICE 交換              雙向資料轉發                DataChannel 關閉
                           Per-socketID Goroutines     所有 Goroutine 退出
```

---

## 五、結論

| 面向 | 價值 |
|---|---|
| Per-socketID Goroutine | 將 O(N×M) 的狀態空間分解為 N 個 O(M)，每個都可獨立推理 |
| 無序 DataChannel | 擁抱現實（亂序不可避免），換取效能與設計一致性 |
| Go 的並發原語 | goroutine + channel + select 完美契合「每個連線一個處理單元」的架構 |
| Go 的部署特性 | 單一二進位、跨平台編譯，對 CLI 工具是理想選擇 |
| pion/webrtc | 純 Go、功能完整、社群成熟 |

**Go 不只是「能用」的語言，而是讓這個架構的核心複雜度被語言層級的並發模型直接消化的語言。** 選擇 Go，不是因為它流行，而是因為它的 goroutine + channel 模型恰好是這個問題最自然的表達方式。
