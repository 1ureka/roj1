# DataChannel 背壓控制：`bufferedAmountLowThreshold` 在 Go 架構中的規畫方針

> 源自 draft.md 中的問題：「對了，我在 TS 時現時，是有根據最佳實踐用 bufferedAmountLowThreshold 配合 bufferedAmountLow 的， Go 需要嗎?」

---

## 一、問題背景：TS 中的最佳實踐是什麼

在瀏覽器的 WebRTC API 中，DataChannel 的 `send()` 是**非阻塞**的 — 資料被丟進 SCTP 層的緩衝區後立即返回。如果發送速率遠超過網路吞吐量，`bufferedAmount` 會持續累積，最終導致：

1. **記憶體暴漲** — 所有未送出的資料都積壓在緩衝區中
2. **訊息被丟棄或連線崩潰** — 部分瀏覽器實作在緩衝區超限時會直接關閉 DataChannel

因此 TS（瀏覽器端）的最佳實踐是：

```typescript
const DC_BUFFER_THRESHOLD = 64 * 1024; // 64 KB

dc.bufferedAmountLowThreshold = DC_BUFFER_THRESHOLD;

function trySend(data: ArrayBuffer) {
    if (dc.bufferedAmount > DC_BUFFER_THRESHOLD) {
        // 暫停發送，等待 bufferedamountlow 事件
        dc.onbufferedamountlow = () => {
            dc.onbufferedamountlow = null;
            dc.send(data);
        };
    } else {
        dc.send(data);
    }
}
```

核心邏輯：**在發送前檢查 `bufferedAmount`，超過閾值就暫停，等 `onbufferedamountlow` 事件回呼後再恢復** — 這是一個典型的事件驅動背壓（backpressure）機制。

---

## 二、Go（pion/webrtc）中是否需要同樣的機制

### 答案：需要。而且同樣重要。

pion/webrtc 的 `DataChannel.Send()` 同樣是**非阻塞**的。它把資料推入底層 SCTP association 的發送緩衝區後就返回，不會等待資料真正被網路發出。這意味著：

- 如果 TCP → DataChannel 方向的讀取速率遠大於 DataChannel 的實際送出速率（例如 host 的目標服務回應極快，但 P2P 連線頻寬有限），緩衝區會無限膨脹。
- pion/webrtc 的 SCTP 實作中，過大的 bufferedAmount 會導致記憶體壓力，嚴重時可能觸發 association 層級的 error。

**不做背壓控制 = 記憶體洩漏定時炸彈。** 這一點與 TS 完全一致，跟語言無關，是 DataChannel（SCTP）傳輸層的固有特性。

### pion/webrtc 提供的對應 API

pion/webrtc 完整實作了 W3C 規範中的背壓 API：

```go
// 設定閾值
dc.SetBufferedAmountLowThreshold(64 * 1024) // 64 KB

// 註冊回呼
dc.OnBufferedAmountLow(func() {
    // 當 bufferedAmount 降到閾值以下時觸發
})

// 查詢當前緩衝量
dc.BufferedAmount() // 返回 uint64
```

所以工具本身是現成的，問題在於：**如何把它融入目前的架構設計中**。

---

## 三、與目前架構的交叉分析

### 3.1 影響的環節

回顧 plan1 中定義的資料路徑，背壓控制影響的是 **TCP Read → DataChannel Send** 這個方向：

```
TCP 連線 ──Read──→ 原始位元組
                      ↓
               [ 封裝成封包 ]
                      ↓
         DataChannel.Send(message)  ← ⚠️ 這一步可能因為對端消化不了而積壓
```

反方向（DataChannel → TCP Write）不需要專門的背壓機制，因為 `net.Conn.Write` 本身是**阻塞的** — 當 TCP 發送緩衝區滿時，Write 會自然阻塞，自動形成背壓。

### 3.2 受影響的元件

根據 plan1 與 draft4 確立的目錄結構，背壓邏輯涉及：

| 元件 | 位置 | 角色 |
|---|---|---|
| Per-SocketID Handler | `tunnel/handler.go` | TCP Read 迴圈內呼叫 `dc.Send()` 的地方 |
| TCP Reader | `tunnel/tcpreader.go` | 將 blocking Read 轉為 channel 的 goroutine |
| 分發器 | `tunnel/dispatcher.go` | 不直接受影響，但若所有 handler 都被背壓暫停，分發器的路由 channel 可能間接滿載 |

### 3.3 為什麼不能直接照搬 TS 的寫法

TS 的背壓是**事件回呼式**的：

```
send → 檢查 bufferedAmount → 超過 → 等 事件 → 恢復
```

但在目前的架構中，per-socketID goroutine 的 TCP → DataChannel 方向是一個**顯式迴圈**（draft3 確立的設計）：

```go
// plan1 中 TCP → DataChannel 的迴圈（簡化）
for {
    n, err := tcpConn.Read(buf)
    if n > 0 {
        pkt := frame(DATA, socketID, nextSeq(&seq), buf[:n])
        dc.Send(pkt)  // ← 需要在這裡加入背壓控制
    }
}
```

如果在這裡直接用 `dc.OnBufferedAmountLow` 的回呼，會面臨一個問題：**goroutine 正在 for 迴圈裡阻塞在 `tcpConn.Read`，它不是事件驅動的**。你不能像 TS 那樣「掛一個回呼然後 return」— goroutine 需要一個明確的阻塞點來暫停自己。

---

## 四、Go 慣用解法：Channel 訊號 + `select` 整合

核心思路：**將 `OnBufferedAmountLow` 回呼轉化為 channel 訊號，讓 goroutine 可以在 `select` 中等待它。**

### 4.1 建立背壓訊號 Channel

```go
// 在 DataChannel 初始化時設定（全域，所有 socketID 共享同一條 DataChannel）
const highWaterMark = 256 * 1024  // 256 KB — 超過此值暫停發送
const lowWaterMark  = 64 * 1024   // 64 KB — 降至此值恢復發送

sendReady := make(chan struct{}, 1) // buffered channel，避免回呼阻塞

dc.SetBufferedAmountLowThreshold(lowWaterMark)
dc.OnBufferedAmountLow(func() {
    select {
    case sendReady <- struct{}{}:
    default: // 已有訊號在 channel 中，不重複發送
    }
})
```

### 4.2 在 Per-SocketID Handler 的 TCP Read 迴圈中整合

根據 draft3 確立的顯式讀寫迴圈，以及 plan1 的 handler 結構，背壓控制自然地嵌入 `select`：

```go
func tcpToDataChannel(ctx context.Context, tcpConn net.Conn, dc *webrtc.DataChannel,
    socketID uint32, seq *uint32, sendReady <-chan struct{}) {

    buf := make([]byte, maxPayloadSize)
    for {
        // 背壓檢查：如果緩衝區已超過高水位，先等待降至低水位
        if dc.BufferedAmount() > highWaterMark {
            select {
            case <-sendReady:
                // 緩衝區已降至低水位，繼續發送
            case <-ctx.Done():
                return
            }
        }

        tcpConn.SetReadDeadline(time.Now().Add(readTimeout))
        n, err := tcpConn.Read(buf)
        if n > 0 {
            pkt := frame(DATA, socketID, nextSeq(seq), buf[:n])
            dc.Send(pkt)
        }
        if err != nil {
            if !isTimeout(err) {
                sendPacket(dc, CLOSE, socketID, nextSeq(seq), nil)
                return
            }
        }
    }
}
```

### 4.3 為什麼這比 TS 的寫法更乾淨

| 面向 | TS（事件回呼） | Go（channel + select） |
|---|---|---|
| 暫停機制 | 掛回呼 → return → 等事件再繼續 | `select` 阻塞在 `sendReady` channel 上 |
| 取消支援 | 需額外處理（移除回呼、清理狀態） | `ctx.Done()` 在同一個 `select` 中，天然支援 |
| 程式流程 | 非線性（回呼鏈） | 線性（for 迴圈內的一個 if + select） |
| 多 socketID 共享 | 需手動管理哪些 socket 在等待 | 每個 goroutine 獨立等待同一個 `sendReady` channel |

**關鍵洞察**：`sendReady` channel 可以被**所有 per-socketID goroutine 共享**，因為它通知的是「DataChannel 的全域緩衝區降至低水位」— 這是 DataChannel 層級的事件，不是 per-socketID 的。當訊號到來時，所有被阻塞的 goroutine 中會有一個被喚醒並恢復發送，其餘的在下一輪迴圈中再次檢查 `BufferedAmount()`，若仍超標則繼續等待。

但這裡有一個微妙之處：**buffered channel 大小為 1，每次回呼只喚醒一個 goroutine**。如果有 N 個 goroutine 同時被背壓阻塞，只有一個能立即恢復。不過這恰恰是正確的行為 — 你不會想在緩衝區剛降到低水位時讓所有 goroutine 同時灌入資料，那會立刻再次突破高水位。逐一喚醒形成**自然的公平調度**，實際效果是按照 goroutine 被調度的順序輪流發送。

---

## 五、水位值的選擇

### 5.1 高水位（High Water Mark）

高水位決定了「何時開始暫停發送」。考量因素：

- DataChannel 底層是 SCTP over DTLS over UDP。SCTP 的發送緩衝區大小在 pion 的預設配置中通常在 **1 MB** 左右。
- 高水位不能太接近 SCTP 的緩衝上限，否則可能觸發底層 error。
- 高水位也不能太低，否則會頻繁觸發背壓，降低吞吐量。

建議起始值：**256 KB**（SCTP 緩衝的約 1/4），後續可根據實測調整。

### 5.2 低水位（Low Water Mark）

低水位決定了「何時恢復發送」。高低水位之間的差距構成一個**遲滯區間（hysteresis band）**，避免在臨界值附近反覆震盪：

- 差距太小 → 頻繁暫停/恢復，產生抖動
- 差距太大 → 恢復太晚，DataChannel 有一段時間閒置，浪費頻寬

建議起始值：**64 KB**（高水位的 1/4）。

### 5.3 與封包大小的關係

plan1 中的封包格式有 9 bytes 的 header（Type 1B + SocketID 4B + SeqNum 4B），payload 大小由 TCP Read 的 buffer 決定。draft3 強調了「你控制 buf 的大小」是顯式迴圈的優勢之一。

這裡有一個權衡：

- **較大的 maxPayloadSize**（例如 64 KB）→ 每次 `dc.Send()` 推入更多資料，bufferedAmount 增長更跳躍，背壓粒度較粗
- **較小的 maxPayloadSize**（例如 16 KB）→ 更細粒度的背壓控制，但封包 header 的 overhead 佔比增加

建議 **maxPayloadSize = 16–32 KB**，在背壓粒度和封裝效率之間取得平衡。具體值在效能測試階段確定。

---

## 六、與 handler 主迴圈的整合

回顧 plan1 定義的 per-socketID handler 結構，背壓邏輯存在於 TCP Read 方向。有兩種整合方式：

### 方式 A：獨立的 TCP → DataChannel goroutine（plan1 的 `tcpreader.go` 擴展）

```go
// handler 主迴圈只處理 DataChannel → TCP 方向
// TCP → DataChannel 由獨立的 goroutine 負責，背壓邏輯封裝在其中
func hostSocketHandler(ctx context.Context, id SocketID, inbox <-chan Packet,
    dc *webrtc.DataChannel, targetAddr string, sendReady <-chan struct{}) {

    // ... 建立 TCP 連線 ...

    go tcpToDataChannel(ctx, tcpConn, dc, id, &seq, sendReady) // 獨立 goroutine，含背壓

    for {
        select {
        case pkt := <-inbox:
            // DataChannel → TCP（不受背壓影響）
        case <-ctx.Done():
            return
        }
    }
}
```

**優點**：背壓邏輯完全封裝在 `tcpToDataChannel` 函數中，handler 主迴圈不受干擾。
**缺點**：每個 socketID 多一個 goroutine（但 Go 中這不是問題）。

### 方式 B：融入 handler 的 `select` 主迴圈

```go
for {
    select {
    case pkt := <-inbox:
        // DataChannel → TCP
    case data := <-tcpReadCh:
        if dc.BufferedAmount() > highWaterMark {
            // 暫停處理 tcpReadCh，等待 sendReady
            select {
            case <-sendReady:
            case <-ctx.Done():
                return
            }
        }
        sendPacket(dc, DATA, id, nextSeq(&seq), data)
    case <-ctx.Done():
        return
    }
}
```

**優點**：所有邏輯在單一 `select` 中，goroutine 數量最少。
**缺點**：在等待 `sendReady` 期間，`inbox` 的封包也被阻塞（因為外層 `select` 沒有在跑）。這代表如果 host → client 方向的背壓觸發了，DataChannel → TCP 方向的資料也會停滯 — **雙向耦合**。

### 建議：方式 A

**方式 A 是更正確的選擇**，理由如下：

1. **雙向解耦**：DataChannel → TCP 的處理不應被 TCP → DataChannel 的背壓拖累。這兩個方向是獨立的資料流，各自的阻塞不應影響對方。
2. **與 draft3 一致**：draft3 中的 `tcpToDataChannel` 本來就是一個獨立的函數概念，只是 plan1 在處理時用了 `tcpReadCh` channel 間接地融進了主迴圈。加入背壓後，讓它回歸為獨立 goroutine 更自然。
3. **goroutine 成本可忽略**：即便每個 socketID 變成 2 個 goroutine（主 handler + TCP reader/sender），數千個連線也只是數千個輕量級 goroutine，完全在 Go runtime 的舒適區內。

---

## 七、邊界情況

### 7.1 背壓期間收到 CLOSE

如果某個 socketID 的 `tcpToDataChannel` goroutine 正在等待 `sendReady`，而 handler 主迴圈收到了對端的 `CLOSE` 封包：

- Handler 取消該 socketID 的 context → `tcpToDataChannel` 透過 `ctx.Done()` 退出
- 不會有資源洩漏

### 7.2 所有 goroutine 同時被背壓

如果 DataChannel 的頻寬極低，所有活躍的 socketID 都被背壓暫停：

- TCP Read 端被暫停 → TCP 連線的接收緩衝區滿 → 對端（本地應用或遠端服務）的 TCP 發送也會被阻塞
- 這是**正確的端到端背壓傳遞**：DataChannel 的頻寬瓶頸最終傳遞到了 TCP 的兩端應用程式

### 7.3 DataChannel 斷開

如果 DataChannel 在背壓等待期間斷開：

- `dataChannelCtx` 取消 → 所有 goroutine 的 `ctx.Done()` 觸發 → 全部退出
- 與 plan1 第十一節定義的生命週期管理一致

### 7.4 `sendReady` channel 的競爭

多個 goroutine 同時 `select` 在同一個 `sendReady` channel 上，只有一個會被喚醒。Go 的 `select` 在多個 case 就緒時會隨機選擇，因此長期來看是公平的。但在極端情況下（某個 goroutine 的資料量遠大於其他），可能需要更精細的調度。

**對於 v1 而言，這個行為已經足夠好。** 如果未來出現公平性問題，可以引入 per-socketID 的令牌桶（token bucket）或加權公平佇列（WFQ），但那是效能調優階段的事。

---

## 八、與既有設計文件的相容性確認

| 設計文件 | 相容性 | 說明 |
|---|---|---|
| draft2（Per-SocketID Goroutine） | ✅ 完全相容 | 背壓邏輯封裝在各 goroutine 內部，不影響分發器的路由邏輯 |
| draft3（顯式讀寫迴圈） | ✅ 完全相容 | 背壓檢查是顯式迴圈中的一個 `if + select`，是 draft3 所主張的「你有機會檢查額外條件」的具體實現 |
| draft4（目錄結構） | ✅ 完全相容 | 背壓邏輯位於 `tunnel/handler.go` 或 `tunnel/tcpreader.go` 內，不需要新增 package |
| plan1（完整計畫） | ✅ 相容，需小幅更新 | `sendReady` channel 作為參數傳入 handler；`tcpToDataChannel` 從 channel-based reader 模式改為獨立 goroutine 含背壓迴圈 |

### 對 plan1 的具體修訂建議

1. **第六節（Host Handler）** 和 **第七節（Client Handler）**：`tcpReadCh` 模式改為獨立的 `tcpToDataChannel` goroutine，背壓邏輯封裝其中。
2. **新增初始化步驟**：在 DataChannel open 後、進入資料轉發之前，設定 `bufferedAmountLowThreshold` 和 `OnBufferedAmountLow` 回呼，建立 `sendReady` channel。
3. **第九節（內部結構圖）** 更新：

```
                ┌──────────────────────────────────────┐
                │     Per-SocketID Goroutine（主迴圈）   │
                │                                        │
  inbox ──────→ │  [Reassembler]                         │
  (channel)     │       │                                │
                │       ├── CONNECT → net.Dial           │
                │       ├── DATA    → tcpConn.Write      │
                │       └── CLOSE   → tcpConn.Close      │
                │                                        │
  ctx.Done ───→ │  清理並退出                              │
                │                                        │
                └──────────────────────────────────────┘
                             ▲ ctx 共享
                ┌──────────────────────────────────────┐
                │     TCP → DataChannel Goroutine       │
                │                                        │
                │  for {                                 │
                │    if BufferedAmount > 高水位 {        │
                │      select: sendReady / ctx.Done     │
                │    }                                   │
                │    tcpConn.Read → 封裝 → dc.Send       │
                │  }                                     │
                │                                        │
  sendReady ──→ │  背壓恢復訊號（所有 socketID 共享）     │
  ctx.Done ───→ │  清理並退出                              │
                │                                        │
                └──────────────────────────────────────┘
```

---

## 九、總結

| 問題 | 回答 |
|---|---|
| Go（pion/webrtc）需要 `bufferedAmountLowThreshold` 嗎？ | **需要。** DataChannel 的非阻塞 `Send()` + 有限的 SCTP 緩衝區 = 不做背壓就是記憶體洩漏。這與語言無關，是傳輸層特性。 |
| pion/webrtc 有對應的 API 嗎？ | **有。** `SetBufferedAmountLowThreshold()` + `OnBufferedAmountLow()` + `BufferedAmount()`，介面與瀏覽器 API 一一對應。 |
| 實作方式是否和 TS 一樣？ | **機制相同，但慣用寫法不同。** TS 用事件回呼暫停/恢復，Go 用 channel 訊號 + `select` 阻塞/喚醒。Go 的寫法更線性、更易推理、天然支援 context 取消。 |
| 對現有架構的影響大嗎？ | **極小。** 背壓邏輯完全封裝在 `tcpToDataChannel` goroutine 內，不影響分發器、不影響 DataChannel → TCP 方向、不需要新的 package。 |
| 建議的水位參數？ | 高水位 256 KB、低水位 64 KB、maxPayloadSize 16–32 KB。v1 先以此為起點，效能測試階段再調整。 |

**一句話結論**：背壓控制不是 TS 的特殊需求，而是 DataChannel 的固有要求。Go 的 goroutine + channel 讓它的實作比 TS 更乾淨 — `OnBufferedAmountLow` 回呼變成 channel 訊號，`select` 阻塞取代回呼嵌套，`ctx.Done()` 統一生命週期管理。這是 draft3 所主張的「顯式迴圈的控制權優勢」的又一個具體體現。
