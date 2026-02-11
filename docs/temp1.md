cmd/
  tunnel/
    main.go            // CLI 進入點

internal/
  app/
    host.go            // 啟動 host 所有服務
    client.go          // 啟動 client 所有服務

  config/
    config.go          // CLI 解析後的設定

  signaling/
    ws_server.go       // host 用
    ws_client.go       // client 用
    signaling.go       // 共用介面

  webrtc/
    peer.go            // 建立 peer connection
    channel.go         // data channel 抽象
    message.go         // signaling 用的訊息

  protocol/
    packet.go          // CONNECT / DATA / CLOSE
    codec.go           // encode / decode
    seq.go             // 序號產生器

  socket/
    manager.go         // socketID -> virtual socket
    virtual_socket.go  // 每一個 socketID 的狀態機
    buffer.go          // 尚未 CONNECT 的 DATA buffer
    reassembler.go     // 依序重組封包

  tcp/
    dialer.go          // host 端連到真實 port
    listener.go        // client 的虛擬服務
    pipe.go            // tcp <-> webrtc 的資料轉發

  util/
    hash.go            // 4-tuple -> socketID
    log.go
