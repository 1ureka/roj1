# 1ureka.net.p2p

**Share any TCP service — Minecraft, AI APIs, or databases — directly and securely without port forwarding or static IPs.**

## Features

- **Zero Cost:** No subscription, no bandwidth limits, and no infrastructure fees.
- **Single Binary:** Written in **Go**. No drivers, runtimes, or installations required.
- **Direct P2P:** Your data stays between you and your peer; no traffic passes through a central server.
- **NAT Traversal:** Works behind most home routers and mobile hotspots using STUN.
- **Lightweight:** The core is built using only the `gorilla/websocket` and `pion/webrtc` libraries.

## Common Use Cases

| Service                | Host Side (Source)                                                                                    | Client Side (Destination)                                                         | Result                                                                                                                               |
| ---------------------- | ----------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Gaming (Minecraft)** | Run your dedicated server on port **25565** and start the CLI as **Host**.                            | Join using the "room code" and map it to local port **25565**.                    | Your friend joins by entering **`127.0.0.1`** in Minecraft. No Hamachi or laggy relays, even for heavy modpacks.                     |
| **AI & LLM Services**  | On your **machine with a GPU**, launch your AI API (e.g., Ollama on **11434**) and start as **Host**. | Join using the "room code" and map it to **any available local port** you prefer. | You can access your local GPU power by pointing their apps to **`127.0.0.1:<your_port>`** as if the AI were running on their own PC. |
| **Self-Hosted Tools**  | Run collaboration platforms like **Mattermost, Wiki.js, or Penpot** locally.                          | Same as above                                                                     | Teams can collaborate on self-hosted instances in real-time without deploying to a cloud provider or VPS.                            |

---

## Getting Started

### Prerequisites (Host Only)

The **Host** needs [Visual Studio Code](https://code.visualstudio.com/) installed to handle the initial handshake via its native Port Forwarding feature (requires a GitHub login).

### Setup

1. **Download:** Get the latest binary for your OS from the [Releases page](https://github.com/1ureka/1ureka.net.p2p/releases).
2. **Launch:** Run the executable in your terminal.

#### For the Host:

1. Select **Host** in the CLI.
2. Enter the **target port** of your local service (e.g., `25565`).
3. In VS Code, go to the **Ports** panel, forward the port shown in the CLI, and set its visibility to **Public**.
4. Share the generated **Forwarded Address** (the URL) with the Client.
5. Once the Client connects, you can stop forwarding the port in VS Code and the P2P tunnel will continue to work without it.

#### For the Client:

1. Select **Client** in the CLI.
2. Paste the **URL** provided by the Host.
3. Enter a **local port** where you want the service to appear on your machine.
4. Access the service at `127.0.0.1:<local_port>`.

---

## Network Compatibility

- **Optimal:** Home Fiber/Broadband, Wi-Fi, 4G/5G mobile hotspots.
- **Limited:** Strict corporate firewalls (Symmetric NAT) or public Wi-Fi that blocks P2P traffic.

## Support

Report bugs or suggest features via [GitHub Issues](https://github.com/1ureka/1ureka.net.p2p/issues). Please include your OS version and any error logs.
