1. 詢問是 host 還是 client

---

host:

1. 填寫要轉發的 port

2. 建立 ws server，並印出 ws server 的 port 與生成的 PIN 碼
(並提示說可用 VS Code 轉發出去)

3. 等待 client 連線

client:

1. 填寫要連線的 ws server url
(placeholder 顯示一個 VS Code 轉發的 url 格式範例，暗示這通常是 host 用 VS Code 轉發出去的)
(例如 wss://***.asse.devtunnels.ms/ws?pin=1234)

2. 填寫該虛擬服務要監聽的 port

3. 建立 ws client

---

host:

1. 接收到 client 連線後，開始 webRTC 連線的 signaling 流程

client:

1. 配合 host 的 signaling 流程，完成 webRTC 連線

---

1. 關閉 ws 連線，並清理資源

---

host:

1. 根據收到的封包，有三種可能 CONNECT, CLOSE, DATA
(包括 4-tuple 所壓縮後的 hash id，以下的邏輯都是針對單個 hash id 的，我們稱作 socketID)
(實際上，封包內也有序號)

2. CONNECT: 建立 tcp 連線到一開始填寫的 port，並把這個 tcp 連線和這個 client 的 webRTC 連線綁在一起
(包括該 socketID 的重組器、緩衝區等服務)
(如果已經有 tcp 連線了，應該 ?)
(如果連線失敗，傳回 CLOSE 封包給 client)
(如果還沒建立連線前，就已經有 DATA 封包了，應該要有機制先存放這些封包，等連線建立好了再送到 host 的服務)
(如果還沒建立連線前，就已經有 CLOSE 封包了，且該封包的序號比 CONNECT 封包晚 (只是先到)，則清空該 socketID 緩衝直到有新的 CONNECT 封包)

3. CLOSE: 關閉這個 socketID 的 tcp 連線，並解除和 client 的 webRTC 連線的綁定
(如果已經沒有 tcp 連線了，忽略，但在日誌紀錄)

4. DATA: 把封包內的資料轉發到這個 socketID 綁定的 tcp 連線上
(如果沒有 tcp 連線了，忽略，但在日誌紀錄)

client:

1. 建立虛擬服務監聽一開始填入的 port，我們稱它 "虛擬服務"

2. 當虛擬服務接收到連線時，根據連線的 4-tuple 計算出 hash id，稱為 socketID
(socketID 只是用於識別，不須還原，因此應該盡可能在可接受的碰撞率下，壓縮成越小越好)

3. 虛擬服務會將 CONNECT 封包送到 host 的 webRTC 連線上，但不等待，後續有任何 data 就直接透過 DATA 封包繼續傳送
(每個新的 socketID 發生時，它的序號產生器、重組器、緩衝區等服務都要初始化)

---

(
不只是因為 webRTC 設為無序可能效能更好，更是因為像我說的在 client 設計上，由於你不可能"推遲握手完成"直到 host 也建立好對應 id 的 socket，因此可以肯定客戶端的應用程式是立刻握手成功，後續的 DATA 立刻開始送，即便 Ordered: true，還是有機會 DATA 比 CONNECT 先送到，因此與其修修補補，不如一開始就以無序設計

所以有一點我要補充，在一個完整的單個 CLI session，只會有一個 dataChannel 存在，且它無序
)

(
STUN 採用 { urls: "stun:stun.l.google.com:19302" }, { urls: "stun:stun1.l.google.com:19302" }
不支援 TURN，因為這個工具的目標是免部屬、免費使用
)

(
DataChannel 斷線後，要看情況，由於我對 Go 的 webRTC 不熟，因此底下我用 TS 的角度來描述
在 TS 中，所謂斷線其實有兩種可能，一種是 PeerConnection 變成 disconnected，而這種情況通常是暫時性的，可能會自動重連成功，因此應該先不急著結束整個 session，而另一種情況是 PeerConnection 變成 closed 或 failed，這種情況就應該直接結束整個 session
但在 TS 中，通常也不用手動判斷，因為大部分的實作下，當 PeerConnection 變成 disconnected 時，DataChannel 並不會直接變成 closed
)

(
對了，我在 TS 時現時，是有根據最佳實踐用 bufferedAmountLowThreshold 配合 bufferedAmountLow 的， Go 需要嗎?
)
