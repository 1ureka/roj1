# 目錄結構審查：`temp1.md` 初始設計 vs 現行架構方向

> 基於 draft2（Per-SocketID Goroutine 模型）、draft3（顯式讀寫迴圈）、plan1（完整計畫）對初始目錄結構的逐項審查

---

## 一、結論先行

temp1 的目錄結構**已大幅過時**。它是在「集中式狀態管理」的心智模型下設計的，而現行方向是「Per-SocketID Goroutine + 顯式迴圈」 — 兩者對模組邊界的切法根本不同。

主要問題：

1. `socket/` 和 `tcp/` 的職責劃分基於舊的分層思維，與 goroutine 模型衝突
2. `pipe.go`（`io.Copy` 式串接）已被明確否決
3. `buffer.go` 和 `reassembler.go` 作為獨立檔案存在於 `socket/` 下，但在新模型中它們是 goroutine 的內部元件
4. `manager.go`（集中式管理器）正是 draft2 要消滅的設計
5. 缺少「分發器」這個核心元件的位置

---

## 二、逐檔案分析

### ✅ 保留不變

| 檔案 | 狀態 | 說明 |
|---|---|---|
| `cmd/tunnel/main.go` | ✅ 保留 | CLI 進入點，不受架構影響 |
| `internal/app/host.go` | ✅ 保留 | 組裝 host 端所有服務的入口 |
| `internal/app/client.go` | ✅ 保留 | 組裝 client 端所有服務的入口 |
| `internal/config/config.go` | ✅ 保留 | CLI 參數解析，純工具性質 |
| `internal/signaling/ws_server.go` | ✅ 保留 | Host 的 WS server |
| `internal/signaling/ws_client.go` | ✅ 保留 | Client 的 WS client |
| `internal/signaling/signaling.go` | ✅ 保留 | 共用介面 |
| `internal/webrtc/peer.go` | ✅ 保留 | PeerConnection 建立 |
| `internal/webrtc/message.go` | ✅ 保留 | Signaling 用的 SDP/ICE 訊息結構 |
| `internal/protocol/packet.go` | ✅ 保留 | CONNECT / DATA / CLOSE 定義 |
| `internal/protocol/codec.go` | ✅ 保留 | 封包 encode / decode |
| `internal/util/hash.go` | ✅ 保留 | 4-tuple → socketID 雜湊 |
| `internal/util/log.go` | ✅ 保留 | 日誌工具 |

### ❌ 應移除

| 檔案 | 原始用途 | 移除理由 |
|---|---|---|
| `internal/webrtc/channel.go` | DataChannel 抽象 | 過度抽象。DataChannel 的介面已由 pion/webrtc 定義，額外包一層只會增加間接性。分發器直接使用 `dc.OnMessage` 即可，不需要自己的抽象層 |
| `internal/socket/manager.go` | socketID → virtual socket 的集中管理 | **這正是 draft2 要消滅的東西。** 集中式管理器意味著共享狀態 + 鎖。在 Per-SocketID Goroutine 模型中，分發器只需要一個 `map[SocketID]chan Packet` 路由表，不需要一個「manager」對象來管理生命週期 |
| `internal/socket/virtual_socket.go` | 每個 socketID 的狀態機 | 名稱暗示的是一個被動的資料結構（被 manager 驅動的 FSM）。在新模型中，每個 socketID 是一個**主動的 goroutine**，不是一個被外部呼叫方法的物件。用「virtual socket」的實體導向思維去建模一個 goroutine，會導致職責混亂 |
| `internal/socket/buffer.go` | 尚未 CONNECT 的 DATA 緩衝 | 在 Per-SocketID Goroutine 中，這只是 handler function 內的一個 `[]Packet` 局部變數，不值得獨立成檔案，更不該獨立成 package |
| `internal/tcp/pipe.go` | tcp ↔ webrtc 的資料轉發 | **已被 draft3 明確否決。** `pipe.go` 暗示的是 `io.Copy` 式的管道串接。本專案的資料路徑不是串流對串流，而是封包協議，需要顯式讀寫迴圈 |

### 🔄 需重新定位

| 檔案 | 原始位置 | 問題 | 新定位 |
|---|---|---|---|
| `internal/protocol/seq.go` | protocol 包 | 序號產生器不只是「協議定義」，它是 per-socketID goroutine 的運行時元件 | 移到 `internal/tunnel/` 中，作為 handler 的內部工具 |
| `internal/socket/reassembler.go` | socket 包 | 重組器的歸屬不該是 `socket/`（已不存在），它是封包處理的核心邏輯 | 移到 `internal/tunnel/` 中，作為 handler 的內部工具 |
| `internal/tcp/dialer.go` | tcp 包 | 只是 `net.Dial` 的薄封裝，獨立成包過度 | 可以直接 inline 在 handler 中，或移到 `internal/tunnel/` |
| `internal/tcp/listener.go` | tcp 包 | Client 虛擬服務的 listener，邏輯簡單但職責明確 | 移到 `internal/tunnel/` 中，作為 client 端的入口元件 |

---

## 三、根本問題：舊結構的心智模型

temp1 的設計暗示了這樣的呼叫流程：

```
app/host.go
  → socket/manager.go              // 集中管理所有 socketID
    → socket/virtual_socket.go     // 被 manager 持有的狀態物件
      → socket/buffer.go           // 被 virtual_socket 使用的緩衝
      → socket/reassembler.go      // 被 virtual_socket 使用的重組器
    → tcp/dialer.go                // 被 manager 呼叫以建立 TCP
    → tcp/pipe.go                  // 被 manager 呼叫以串接資料
```

這是典型的 **OOP 分層架構** — 一個 manager 物件持有一堆 virtual_socket 物件，每個 virtual_socket 持有 buffer 和 reassembler。資料流經各個物件的方法呼叫。

新模型的呼叫流程：

```
app/host.go
  → tunnel/dispatcher.go           // 單一 goroutine，讀 DataChannel 並路由
    → tunnel/handler.go            // 每個 socketID 一個 goroutine
      （內含 reassembler, seq, pending — 全是局部變數）
      （直接使用 net.Dial, net.Conn, dc.Send）
```

**差異的本質**：舊模型把「socketID 的處理」拆成了 5 個跨 package 的檔案（manager, virtual_socket, buffer, reassembler, pipe）。新模型把它收斂成一個 goroutine function，所有狀態都是函數局部變數。

分散不等於解耦。在這個場景中，buffer、reassembler、tcp pipe 之間有**極強的時序依賴** — 它們必須在同一個 goroutine 的 select 迴圈中協調。把它們拆到不同檔案/package 只會讓這個時序邏輯變得更難看清。

---

## 四、建議的新目錄結構

```
cmd/
  tunnel/
    main.go                // CLI 進入點

internal/
  app/
    host.go                // 組裝 host 端：signaling → WebRTC → dispatcher
    client.go              // 組裝 client 端：signaling → WebRTC → listener + dispatcher

  config/
    config.go              // CLI 參數解析

  signaling/
    server.go              // Host 端 WS server（含 PIN 驗證）
    client.go              // Client 端 WS client
    message.go             // SDP / ICE 訊息結構

  webrtc/
    peer.go                // PeerConnection 建立與 DataChannel 設定

  protocol/
    packet.go              // Packet 結構、Type 常數 (CONNECT / DATA / CLOSE)
    codec.go               // Encode / Decode

  tunnel/
    dispatcher.go          // 分發器：DataChannel → 路由到 per-socketID channel
    handler.go             // Per-SocketID Goroutine 的核心邏輯（host 與 client 共用骨架）
    reassembler.go         // 重組器（被 handler 作為局部元件使用）
    seq.go                 // 序號產生器
    listener.go            // Client 端虛擬服務的 TCP listener
    tcpreader.go           // TCP 讀取 goroutine（將 blocking Read 轉為 channel）

  util/
    hash.go                // 4-tuple → SocketID
    log.go                 // 日誌
```

### 與 temp1 的關鍵差異

| 面向 | temp1 | 新結構 |
|---|---|---|
| 核心 package | `socket/` + `tcp/`（兩個 package 互相依賴） | `tunnel/`（單一 package 收攏所有資料轉發邏輯） |
| socketID 管理 | `manager.go` 集中管理 | `dispatcher.go` 僅做路由，狀態在各 goroutine 內 |
| 狀態表達 | `virtual_socket.go` 物件 | `handler.go` goroutine function + 局部變數 |
| 緩衝 | `buffer.go` 獨立檔案 | `handler.go` 內的 `[]Packet` 局部變數 |
| 資料轉發 | `pipe.go`（io.Copy 式） | `handler.go` 內的顯式 select 迴圈 |
| 重組器 | `socket/reassembler.go` | `tunnel/reassembler.go`（同 package，handler 直接使用） |
| DataChannel 抽象 | `webrtc/channel.go` | 無（直接使用 pion/webrtc 的 API） |
| signaling 訊息 | `webrtc/message.go` | `signaling/message.go`（訊息屬於 signaling 階段） |

### 為什麼是 `tunnel/` 而不是 `socket/` + `tcp/`

1. **`socket/` 暗示的是「管理一堆 socket 物件」**。新模型沒有 socket 物件 — 只有 goroutine。
2. **`tcp/` 作為獨立 package 不合理**。Host 的 `net.Dial` 和 Client 的 `net.Listen` 都只是 handler 內的一兩行呼叫，不值得額外建包。
3. **`tunnel/` 是一個動詞性的名稱**，反映的是「把 TCP 流量隧穿過 DataChannel」這個核心行為。所有與這個行為直接相關的元件（dispatcher、handler、reassembler、tcpreader）自然地聚合在一起。

---

## 五、總結

temp1 的目錄結構是在 **OOP 分層 + `io.Copy` 管道串接**的假設下設計的，而現行方向是 **Per-SocketID Goroutine + 顯式讀寫迴圈**。兩者的模組邊界完全不同：

- 舊模型按「物件種類」切分：manager、virtual_socket、buffer、pipe
- 新模型按「運行時邊界」切分：dispatcher（路由）、handler（per-socketID goroutine）、reassembler（重組工具）

核心改動是：**消滅 `socket/` 和 `tcp/` 兩個 package，統一為 `tunnel/`**，讓分發器和 handler 的邏輯在同一個 package 下清晰可見，而不是散落在跨 package 的物件方法呼叫鏈中。
