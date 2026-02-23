# netscan

![Go Version](https://img.shields.io/badge/go-1.26-00ADD8?style=flat&logo=go)
![Docker](https://img.shields.io/badge/docker-v20.10+-2496ED?style=flat&logo=docker)
![License](https://img.shields.io/badge/license-MIT-green.svg)

---

## Key Features

*   **Automated Discovery**: Randomized ICMP sweeps across multiple subnets to automatically find new devices.
*   **Real-Time Monitoring**
*   **Optional SNMP Enrichment**
*   **InfluxDB Integration**: Native support for InfluxDB v2, separating operational metrics from health telemetry.
*   **Secure Deployment**: Supports rootless execution via capability-based security (`CAP_NET_RAW`).

---

## Docker Quick Start

Get up and running in minutes with the pre-configured Docker stack.

### Prerequisites
*   Docker Engine 20.10+
*   Docker Compose V2

### 1. Clone & Configure
```bash
git clone https://github.com/kljama/netscan.git
cd netscan

# Create config files
cp config.yml.example config.yml
cp .env.example .env
```

### 2. Set Your Network Range
Open `config.yml` and set your **actual** network CIDR.
```yaml
networks:
  - "192.168.1.0/24"  # <--- Change this to your network!
```

### 3. Launch
```bash
docker compose up -d
```
Access the **InfluxDB UI** at `https://localhost` (User: `admin`, Pass: `admin123`).

---

## 🛠️ Deployment Options

| Method | Best For | Description |
|--------|----------|-------------|
| **Docker Compose** | Testing & Small Prod | Easiest setup. Orchestrates netscan + InfluxDB together. |
| **Native Systemd** | Production Security | Runs as dedicated user with minimal capabilities. |

> See **[MANUAL.md](MANUAL.md)** for detailed deployment guides, security hardening, and configuration references.

---

## Verification

Check if the service is running correctly:

```bash
# View live logs
docker compose logs -f netscan

# Check health endpoint
curl http://localhost:8080/health
```

---

## License

MIT License - See [LICENSE.md](LICENSE.md)
