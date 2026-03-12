# InfluxDB Dashboard Templates

This directory contains dashboard templates that are automatically provisioned when the InfluxDB container starts for the first time.

## Dashboard Files

### 1. `netscan.json`

**Purpose:** Primary network monitoring dashboard for the netscan service.

**Provides:**
* **Device Health Stats:** "Discovered Devices", "Good Devices", "Failed Devices", and "Suspended Devices".
* **Latency Analysis:** "Top 10 RTT" and "Bottom 10 RTT" devices.
* **Overall Performance:** "Ping Success Rate (mean)" and "Total ICMP Packets sent".
* **Failed Device Details:** A table listing the IPs of devices that have failed 5+ consecutive pings.

**Source:** User-provided (manually added)

### 2. `influxdb_health.json`

**Purpose:** Application health and performance monitoring for the `netscan` service itself.

**Provides:**
* **Application Metrics:** "Device Count", "Active Pingers", "Device Churn Rate", and "Go-Routines".
* **Resource Usage:** "Go Heap Allocation [MByte]" and "OS-RSS [MByte]".
* **Performance & Throughput:** "ICMP pps sent", "Average Ping Duration [ms]", "Rate Limit Utilization (%)", and "Measured ping_interval" (to compare with config).
* **Database Health:** "InfluxDB Status" and "Failed Batches" (write errors).

**Source:** User-provided (manually added)

### 3. `influxdb_operational_monitoring.yml`

**Purpose:** InfluxDB internal operational monitoring dashboard.

**Provides:**
* InfluxDB task execution metrics
* Database cardinality tracking
* Query performance metrics
* System resource utilization

**Source:** Public community template from [influxdata/community-templates](https://github.com/influxdata/community-templates/tree/master/influxdb2_operational_monitoring)

## Auto-Provisioning

All dashboards in this directory are automatically applied during the InfluxDB initialization process via the `init-influxdb.sh` script. The provisioning happens only on first startup when the database is initialized.

### How It Works

1.  The `docker-compose.yml` mounts this directory as `/templates` inside the InfluxDB container (read-only)
2.  The `init-influxdb.sh` script runs after InfluxDB is ready
3.  Each template file is applied using the `influx apply` command
4.  Dashboards become immediately available in the InfluxDB UI at http://localhost:8086

### Adding Custom Dashboards

To add additional dashboards:

1.  Export your dashboard from the InfluxDB UI (Settings → Templates → Export)
2.  Save the exported JSON/YAML file to this directory
3.  Add an `influx apply` command to the `init-influxdb.sh` script
4.  Rebuild and restart the stack: `docker compose down -v && docker compose up -d`

**Note:** The `-v` flag removes volumes, which forces re-initialization and dashboard provisioning.
