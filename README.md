# 🏮 Roj1 (Roji)

![CI Status](https://github.com/1ureka/roj1/actions/workflows/ci.yml/badge.svg)
<!-- ![Go Report Card](https://goreport.card.com/badge/github.com/1ureka/roj1) -->

`Roj1` (pronounced as *Roji*, Japanese for "alleyway") is a lightweight tool that carves a private, **1-to-1** path between any two points on the internet. Share Minecraft servers, AI APIs, or local databases directly and securely—without port forwarding, static IPs, or middleman fees.


## Why Roj1?

While the public internet becomes increasingly crowded and monetized, **Roj1** offers a quiet, hidden passage.

* **Zero Tolls:** No subscriptions, no bandwidth caps, and no infrastructure costs.
* **Pure P2P:** Your data stays between you and your peer. No traffic ever touches a central relay server.
* **Single Binary:** A single Go binary. No drivers, no complex networking, just a direct link.
* **NAT-Proof:** Effortlessly traverses home routers and mobile hotspots using STUN/WebRTC.
* **Lightweight:** The core is built using only the `gorilla/websocket` and `pion/webrtc` libraries.

---

## Common Use Cases

| Service | The Host (Source) | The Peer (Destination) | The Experience |
| --- | --- | --- | --- |
| **Gaming** | Run Minecraft dedicated server on port `25565`. | Set port `25565` in **Roj1** and establish the connection. | Join the server via `127.0.0.1`. Low latency, no Hamachi bloat, even for heavy modpacks. |
| **AI / LLM** | Host your AI service (e.g., Ollama (`11434`)) on your GPU rig. | Map to **any available local port** in **Roj1** and connect. | Use remote GPU power as if the AI were running natively on your laptop. |
| **Self-Hosted Tools** | Run collaboration platforms like **Mattermost, Wiki.js, or Penpot** locally. | Same as above | Teams can collaborate on self-hosted instances without VPS or cloud deployment. |

---

## Getting Started

### Prerequisites (Host Only)

The **Host** needs [Visual Studio Code](https://code.visualstudio.com/) installed to handle the initial handshake via its native Port Forwarding feature (requires a GitHub login).

### Setup

1. **Download:** Get the latest binary for your OS from the [Releases page](https://github.com/1ureka/roj1/releases).
2. **Launch:** Run the executable in your terminal.

### For the Host:

1. Launch `roj1` and select **Host**.
2. Enter your local service port (e.g., `25565`).
3. In VS Code's **Ports** panel, forward the port provided by the CLI and set visibility to **Public**.
4. Share the generated **Forwarded URL** with your peer.
5. *Once connected, you can stop the VS Code forward; the P2P tunnel is now independent.*

### For the Peer:

1. Launch `roj1` and select **Client**.
2. Paste the **URL** provided by the Host.
3. Choose a local port to map the service to.
4. Access the service at `127.0.0.1:<your_port>`.

---

## Network Compatibility

* **Optimal:** Fiber, Home Wi-Fi, 4G/5G mobile hotspots.
* **Limited:** Strict corporate firewalls (Symmetric NAT) or public Wi-Fi with P2P blocking.

## Support

Report bugs or suggest features via [GitHub Issues](https://github.com/1ureka/roj1/issues). Please include your OS version and any error logs.
